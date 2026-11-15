"""Check the platform and ELF files in the BuildKit OCI layout before publishing."""

import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile


def image_platform(index):
    platforms = []
    for descriptor in index["manifests"]:
        platform = descriptor["platform"]
        os_arch = (platform["os"], platform["architecture"])
        if (os_arch == ("unknown", "unknown") and
                descriptor.get("annotations", {}).get("vnd.docker.reference.type") == "attestation-manifest"):
            continue
        platforms.append("/".join(os_arch))
    if len(platforms) != 1:
        raise ValueError(f"expected one SteamCMD runtime platform, found {platforms}")
    return platforms[0]


def check_executables(platform, directory):
    # The 64-bit Ubuntu userspace runs Valve's 32-bit i386 binary on amd64.
    # Inspect the files on the host; nothing from the image is executed.
    machines = {"linux/amd64": 62, "linux/arm64": 183}
    for name in ("bash", "dpkg", "steamcmd"):
        with (directory / name).open("rb") as binary:
            header = binary.read(20)
        if len(header) != 20 or header[:4] != b"\x7fELF" or header[5] != 1:
            raise ValueError(f"{name}: missing little-endian ELF header")
        machine = int.from_bytes(header[18:20], "little")
        expected = (1, 3) if name == "steamcmd" else (2, machines[platform])
        if (header[4], machine) != expected:
            raise ValueError(f"{name}: {platform} has ELF class/machine {(header[4], machine)}, expected {expected}")
    if platform != "linux/amd64":
        raise ValueError(f"unsupported SteamCMD platform {platform}")


def check_layout(layout, digest):
    source = f"oci:{layout}"
    index = subprocess.check_output(["skopeo", "inspect", "--raw", source])
    if "sha256:" + hashlib.sha256(index).hexdigest() != digest:
        raise ValueError("OCI index does not match the digest selected for publication")
    platform = image_platform(json.loads(index))
    if platform not in {"linux/arm64", "linux/amd64"}:
        raise ValueError(f"unsupported SteamCMD platform {platform}")
    # Select the sole runtime child from the same index. Docker's image ID is
    # the config digest, so the stopped container uses those filesystem layers.
    image = "gameplane-steamcmd:platform-check"
    subprocess.run(["skopeo", "--override-os", "linux", "--override-arch", platform.split("/")[1],
                    "copy", source, f"docker-daemon:{image}"], check=True)
    config = json.loads(subprocess.check_output(["docker", "image", "inspect", image]))[0]
    if f"{config['Os']}/{config['Architecture']}" != platform:
        raise ValueError("OCI child configuration disagrees with the index platform")
    container = subprocess.check_output(["docker", "create", "--platform", platform, "--network", "none",
                                         "--entrypoint", "/bin/false", config["Id"]], text=True).strip()
    try:
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            for path in ("/bin/bash", "/usr/bin/dpkg", "/usr/lib/games/steam/steamcmd"):
                subprocess.run(["docker", "cp", "-L", f"{container}:{path}", str(directory / Path(path).name)],
                               check=True)
            check_executables(platform, directory)
    finally:
        subprocess.run(["docker", "rm", "-v", container], check=True)
    print(f"Verified {digest}: {platform}, amd64 Bash and dpkg, i386 SteamCMD")


if __name__ == "__main__":
    check_layout(*sys.argv[1:])

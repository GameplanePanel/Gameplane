import tempfile
import unittest
from pathlib import Path

from check_steamcmd_platform import check_executables, image_platform


class SteamCMDPlatformTest(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.directory = Path(temp.name)
        for name, elf_class, machine in (("bash", 2, 62), ("dpkg", 2, 62), ("steamcmd", 1, 3)):
            header = bytearray(20)
            header[:6] = b"\x7fELF" + bytes([elf_class, 1])
            header[18:20] = machine.to_bytes(2, "little")
            (self.directory / name).write_bytes(header)

    def test_amd64_with_i386_steamcmd(self):
        check_executables("linux/amd64", self.directory)

    def test_arm64_label_with_x86_files(self):
        # The original index and config both said arm64. Inspecting Bash catches
        # the bad rootfs even when the two pieces of metadata agree.
        platform = image_platform({"manifests": [{"platform": {"os": "linux", "architecture": "arm64"}}]})
        with self.assertRaisesRegex(ValueError, "bash: linux/arm64 has ELF"):
            check_executables(platform, self.directory)

    def test_wrong_steamcmd_binary(self):
        (self.directory / "steamcmd").write_bytes((self.directory / "bash").read_bytes())
        with self.assertRaisesRegex(ValueError, "steamcmd: linux/amd64 has ELF"):
            check_executables("linux/amd64", self.directory)

    def test_extra_platform_is_rejected_with_attestations_present(self):
        amd64 = {"platform": {"os": "linux", "architecture": "amd64"}}
        attestation = {"platform": {"os": "unknown", "architecture": "unknown"},
                       "annotations": {"vnd.docker.reference.type": "attestation-manifest"}}
        descriptors = [amd64, attestation]
        self.assertEqual(image_platform({"manifests": descriptors}), "linux/amd64")
        descriptors.append({"platform": {"os": "linux", "architecture": "arm64"}})
        with self.assertRaisesRegex(ValueError, "one SteamCMD runtime platform"):
            image_platform({"manifests": descriptors})

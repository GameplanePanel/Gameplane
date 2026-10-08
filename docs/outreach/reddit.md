**Project Name:** Gameplane

**Repo/Website:** [GitHub](https://github.com/GameplanePanel/Gameplane) | [website](https://gameplanepanel.github.io/website/)

**Description:** I needed a single dashboard to easily manage my game server. At first I was using Docker and the most logical choice for me at the time was AMP (CubeCoders). The issue is that this can't run inside a Docker container and wants to be installed on the host. Now that I have switched to Kubernetes I had multiple choices having both Kubernetes and Docker on the same machine for game server (which seemed like a bad idea), run AMP on the host without Docker isolation that is even worse a single compromised server is a host compromised, running VM (using KVM / KubeVirt) for AMP what I was doing at the start, or switch to full Kubernetes native but no GUI specifically for game exist and a lot of my friends won't manage using direct kubectl or even Lens. So I had the idea of creating a full dashboard for managing this.
This is still in beta, Postgres DB is experimental.

**AI Involvement:** This was a project only done by AI, no code was written by human. Every code change was checked by me and while I'm not a web dev and I'm also not really a Go dev nothing seemed out of place or absolutely wrong or bad. For the how the AI was guided on what it needed everything touching web need to go through a design phase first using Pencil and the Pencil MCP. Once validated the AI write the code and open a PR. I then check all PRs once the AI is done and merge all once validated.

**Deployment:**

System requirement:
  - Kubernetes 1.28+
  - Helm 3.13+
  - default RWO StorageClass

Tested only on x86-64 CPU, does not own any ARM (except my phone) or 32-bit CPU.
An ARM image is also built but not live tested.

[Install docs on GitHub](https://github.com/GameplanePanel/Gameplane#install-on-a-cluster) or
```sh
helm upgrade --install gameplane oci://ghcr.io/gameplanepanel/charts/gameplane \
  --version 0.3.0 \
  --namespace gameplane-system --create-namespace \
  --set ingress.host=gameplane.your-domain.test
  # --set ingress.tls=false # if you do not want TLS, If on it require a cert manager annotation or a pre-created TLS cert

kubectl -n gameplane-system exec -i deploy/gameplane-api -- /api bootstrap-admin --username admin --password-stdin
# or
kubectl -n gameplane-system exec deploy/gameplane-api -- /api bootstrap-admin --username admin --password "<choose>"
```

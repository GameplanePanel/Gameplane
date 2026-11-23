# Set a default player hostname per game template

Use the Helm value `operator.templateHostnames` to assign a default hostname to NodePort and LoadBalancer servers using a given template. A hostname set on an individual server takes precedence. [Networking guide](../../docs/networking.md#a-hostname-as-the-servers-address) · [PR #1061](https://github.com/GameplanePanel/Gameplane/pull/1061)

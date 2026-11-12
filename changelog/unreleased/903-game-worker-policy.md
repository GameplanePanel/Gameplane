### Game worker placement and resource limits

Administrators can reserve game workers with a node label and taint, bound CPU
requests and memory reservations and limits, and set per-namespace quotas.
GameServer overrides retain the template quantities for resources they omit.
When the policy takes over a legacy game, the operator drains the existing Pod
before applying placement and resource changes. A verified running revision
uses its saved stop sequence. An unversioned game uses its Pod termination grace.

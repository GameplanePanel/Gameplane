### Browser checks on hosted runners

The dashboard’s mock, live Kubernetes and screenshot jobs now verify that the
locked Chromium build can render a screenshot and record video. Runners with a
working browser skip Ubuntu package installation. If the probe fails, CI
installs the distribution dependencies and repeats the probe before testing.

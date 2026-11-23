package main

import (
	"strings"
	"testing"
)

func TestTemplateHostnameFlag(t *testing.T) {
	hosts := templateHostnameMap{}
	for _, value := range []string{"minecraft-java=mc.example.net", "valheim=val.example.net"} {
		if err := hosts.set(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, invalid := range []string{
		"minecraft-java=another.example.net", "missing-separator", "=host", "bad template=host",
		"game=", "game=https://example.net", "game=example.net:1234", "game=.example.net",
		"game=" + strings.Repeat("a", 64) + ".net", "game=" + strings.Repeat("abc.", 63) + "de",
	} {
		if err := hosts.set(invalid); err == nil {
			t.Errorf("accepted invalid mapping %q", invalid)
		}
	}
	if len(hosts) != 2 || hosts["minecraft-java"] != "mc.example.net" || hosts["valheim"] != "val.example.net" {
		t.Errorf("unexpected template hostnames: %v", hosts)
	}
}

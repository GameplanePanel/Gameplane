package handlers

import "k8s.io/apimachinery/pkg/util/validation"

// validServerName reports whether name can be used for a new GameServer.
// The operator creates Services "<name>" and "<name>-agent", and a Service
// name must be a DNS-1035 label (a lowercase letter first, at most 63
// characters), so the name must start with a letter and have at most 57
// characters. The apiserver alone would accept names such as "7d2d".
func validServerName(name string) bool {
	return len(validation.IsDNS1035Label(name+"-agent")) == 0
}

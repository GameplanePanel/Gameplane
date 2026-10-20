package notify

import (
	"errors"
	"testing"
)

func TestNotificationObservationRetainsLastKnownSinkDecision(t *testing.T) {
	enabled := []Sink{{Enabled: true, ConfigRef: "sink-credential"}}
	observe := notificationObservationEnabled(false, enabled, nil)
	if !observe {
		t.Fatal("configured sink did not enable observers")
	}
	observe = notificationObservationEnabled(observe, nil, errors.New("store temporarily unavailable"))
	if !observe {
		t.Fatal("transient config read cancelled established observers")
	}
	observe = notificationObservationEnabled(observe, nil, nil)
	if observe {
		t.Fatal("successfully removed sinks did not cancel observers")
	}
	if notificationObservationEnabled(false, nil, errors.New("unavailable")) {
		t.Fatal("unknown initial config started polling")
	}
}

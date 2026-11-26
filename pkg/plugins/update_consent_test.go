package plugins

import (
	"reflect"
	"slices"
	"testing"
)

// An update narrowing a filter keeps it working; one widening it waits, the old one still runs.
func TestMQTTUpdateNarrowsAndWidens(t *testing.T) {
	g := Permissions{MQTT: true, MQTTSubscribe: []string{"tuya/#"}, MQTTPublish: []string{}}.Grants()
	allowed, pending := Permissions{MQTT: true, MQTTSubscribe: []string{"tuya/status"}, MQTTPublish: []string{}}.allowedBy(g)
	if !reflect.DeepEqual(allowed.MQTTSubscribe, []string{"tuya/status"}) || pending.MQTT {
		t.Errorf("narrowed: allowed %v, pending %v", allowed.MQTTSubscribe, pending.MQTTSubscribe)
	}

	g = Permissions{MQTT: true, MQTTSubscribe: []string{"tuya/status"}, MQTTPublish: []string{}}.Grants()
	allowed, pending = Permissions{MQTT: true, MQTTSubscribe: []string{"tuya/#"}, MQTTPublish: []string{}}.allowedBy(g)
	if !reflect.DeepEqual(allowed.MQTTSubscribe, []string{"tuya/status"}) || !reflect.DeepEqual(pending.MQTTSubscribe, []string{"tuya/#"}) {
		t.Errorf("widened: allowed %v, pending %v", allowed.MQTTSubscribe, pending.MQTTSubscribe)
	}
	// allowing it: the user consents to what is asked now
	wide := Permissions{MQTT: true, MQTTSubscribe: []string{"tuya/#"}, MQTTPublish: []string{}}
	g.MQTT.Subscribe = []string{"tuya/#"}
	ok := WithinRequest(*g, wide)
	if _, pending = wide.allowedBy(&ok); pending.MQTT {
		t.Errorf("still waiting after allowing: %v", pending.MQTTSubscribe)
	}
}

// An action allowed with its confirmation and updated to run without one waits for the user.
func TestActionLosingConfirmationWaits(t *testing.T) {
	v1 := Permissions{Actions: []string{"all_off", "status"}, ConfirmActions: []string{"all_off"}}
	g := v1.Grants()
	v2 := Permissions{Actions: []string{"all_off", "status"}}
	allowed, pending := v2.allowedBy(g)
	if slices.Contains(allowed.Actions, "all_off") || !slices.Contains(pending.Actions, "all_off") || !slices.Contains(allowed.Actions, "status") {
		t.Errorf("allowed %v, pending %v", allowed.Actions, pending.Actions)
	}
	// stricter is fine
	v3 := Permissions{Actions: []string{"all_off", "status"}, ConfirmActions: []string{"all_off", "status"}}
	if allowed, pending = v3.allowedBy(g); len(pending.Actions) != 0 || len(allowed.Actions) != 2 {
		t.Errorf("more confirmation: allowed %v, pending %v", allowed.Actions, pending.Actions)
	}
	ok := WithinRequest(*g, v2)
	if allowed, pending = v2.allowedBy(&ok); len(pending.Actions) != 0 || len(allowed.Actions) != 2 {
		t.Errorf("after allowing: allowed %v, pending %v", allowed.Actions, pending.Actions)
	}
}

// A filter the user narrowed stays without asking again; an update asking past the narrowed
// one, where it would get nothing, asks.
func TestMQTTUserNarrowedGrant(t *testing.T) {
	asked := Permissions{MQTT: true, MQTTSubscribe: []string{"home/#"}, MQTTPublish: []string{}}
	g := asked.Grants()
	g.MQTT.Subscribe = []string{"home/kitchen/#"}
	if _, pending := asked.allowedBy(g); pending.MQTT {
		t.Errorf("narrowed by the user: pending %v", pending.MQTTSubscribe)
	}
	garage := Permissions{MQTT: true, MQTTSubscribe: []string{"home/garage/#"}, MQTTPublish: []string{}}
	allowed, pending := garage.allowedBy(g)
	if len(allowed.MQTTSubscribe) != 0 || !reflect.DeepEqual(pending.MQTTSubscribe, []string{"home/garage/#"}) {
		t.Errorf("outside the narrowed filter: allowed %v, pending %v", allowed.MQTTSubscribe, pending.MQTTSubscribe)
	}
}

// The install dialog showed an action asking for confirmation; files that dropped it between
// the preview and the download do not get it allowed.
func TestInstallSeenConfirmation(t *testing.T) {
	shown := Permissions{Actions: []string{"all_off", "status"}, ConfirmActions: []string{"all_off"}}
	seen := SeenOf(shown, 2)
	downloaded := Permissions{Actions: []string{"all_off", "status"}}
	g := downloaded.OnlySeen(seen).Grants()
	allowed, pending := downloaded.allowedBy(g)
	if slices.Contains(allowed.Actions, "all_off") || !slices.Contains(pending.Actions, "all_off") || !slices.Contains(allowed.Actions, "status") {
		t.Errorf("allowed %v, pending %v", allowed.Actions, pending.Actions)
	}
	// the same files as shown: allowed
	g = shown.OnlySeen(seen).Grants()
	if allowed, pending = shown.allowedBy(g); len(allowed.Actions) != 2 || len(pending.Actions) != 0 {
		t.Errorf("as shown: allowed %v, pending %v", allowed.Actions, pending.Actions)
	}
}

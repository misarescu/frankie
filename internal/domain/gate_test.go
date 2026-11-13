package domain

import "testing"

func TestCommandGateDetailsPassed(t *testing.T) {
	zero, one := 0, 1
	tests := []struct {
		name string
		d    CommandGateDetails
		want bool
	}{
		{"all exit 0", CommandGateDetails{Commands: []CommandResult{{ExitCode: &zero}, {ExitCode: &zero}}}, true},
		{"one fails", CommandGateDetails{Commands: []CommandResult{{ExitCode: &zero}, {ExitCode: &one}}}, false},
		{"one never ran", CommandGateDetails{Commands: []CommandResult{{ExitCode: &zero}, {}}}, false},
		{"no commands", CommandGateDetails{}, false},
		{"gate error", CommandGateDetails{Commands: []CommandResult{{ExitCode: &zero}}, Error: "clone failed"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.d.Passed(); got != tt.want {
				t.Errorf("Passed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGateEnumsValid(t *testing.T) {
	if !GateCommand.Valid() || GateType("lint").Valid() {
		t.Error("GateType.Valid")
	}
	if !GateBlocking.Valid() || GatePolicy("maybe").Valid() {
		t.Error("GatePolicy.Valid")
	}
	if !GateFail.Valid() || GateVerdict("ok").Valid() {
		t.Error("GateVerdict.Valid")
	}
}

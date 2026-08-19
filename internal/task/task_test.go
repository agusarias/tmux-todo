package task

import "testing"

func TestScopeKindValid(t *testing.T) {
	for _, k := range ScopeKinds() {
		if !k.Valid() {
			t.Errorf("ScopeKinds() returned %q but Valid() is false", k)
		}
	}
	if ScopeKind("project").Valid() {
		t.Error("unknown scope kind reported as valid")
	}
	if ScopeKind("").Valid() {
		t.Error("empty scope kind reported as valid")
	}
}

func TestScopeKindsOrder(t *testing.T) {
	got := ScopeKinds()
	want := []ScopeKind{ScopeSession, ScopeDir, ScopeGlobal}
	if len(got) != len(want) {
		t.Fatalf("got %d kinds, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("kind %d = %q, want %q", i, got[i], want[i])
		}
	}
}

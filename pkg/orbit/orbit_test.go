package orbit

import (
	"testing"
	"time"
)

func TestSeedSoloHostsAgentSymmetric(t *testing.T) {
	r := NewRegistry()
	r.SeedSolo(time.Unix(0, 0))

	nodes := r.List()
	if len(nodes) != 3 {
		t.Fatalf("expected 3 solo nodes, got %d", len(nodes))
	}

	byRole := map[Role]Node{}
	for _, n := range nodes {
		byRole[n.Role] = n
	}

	for _, role := range []Role{RolePlanet, RoleMoon, RoleComet} {
		n, ok := byRole[role]
		if !ok {
			t.Fatalf("missing role %q in solo seed", role)
		}
		if !n.HostsAgent {
			t.Errorf("solo node %q: HostsAgent=false, want true (symmetric to HostsComet)", role)
		}
	}

	if !byRole[RoleComet].HostsComet {
		t.Errorf("comet-local: HostsComet=false, want true")
	}
}

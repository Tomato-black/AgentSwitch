package accesskit

import "testing"

func TestDocDiffAndMask(t *testing.T) {
	oldDoc := `{"env":{"ANTHROPIC_MODEL":"a","ANTHROPIC_AUTH_TOKEN":"secret","HTTP_PROXY":"p1"}}`
	newDoc := `{"env":{"ANTHROPIC_MODEL":"b","ANTHROPIC_AUTH_TOKEN":"secret2","HTTPS_PROXY":"p2"}}`
	lines := DocDiff(oldDoc, newDoc)
	byPath := map[string]DocDiffLine{}
	for _, l := range lines {
		byPath[l.Path] = l
	}
	if l := byPath["env.ANTHROPIC_MODEL"]; l.Op != "~" || l.Old != `"a"` || l.New != `"b"` {
		t.Fatalf("model diff = %+v", l)
	}
	if l := byPath["env.ANTHROPIC_AUTH_TOKEN"]; l.Op != "~" || l.Old != "***" || l.New != "***" {
		t.Fatalf("token must be masked: %+v", l)
	}
	if _, ok := byPath["env.HTTP_PROXY"]; !ok {
		t.Fatal("removed key must appear")
	}
	if _, ok := byPath["env.HTTPS_PROXY"]; !ok {
		t.Fatal("added key must appear")
	}
}

package credential

import "testing"

func TestChannelKey(t *testing.T) {
	if ChannelKey("abc") != "channel:abc" {
		t.Fatal(ChannelKey("abc"))
	}
}

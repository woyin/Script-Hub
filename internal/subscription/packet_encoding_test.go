package subscription

import (
	"testing"
)

func TestVLESSPacketEncoding(t *testing.T) {
	for _, encoding := range []string{"", "none", "xudp", "packetaddr"} {
		t.Run("value_"+encoding, func(t *testing.T) {
			nodes, err := Parse([]byte("vless://test-id@proxy.example:443?packetEncoding=" + encoding + "#test"))
			if err != nil {
				t.Fatal(err)
			}
			if text(nodes[0]["packet-encoding"]) != encoding {
				t.Fatal("encoding lost")
			}
			out, err := Export(nodes, "uri")
			if err != nil {
				t.Fatal(err)
			}
			again, err := Parse(out.Body)
			if err != nil || text(again[0]["packet-encoding"]) != encoding {
				t.Fatal("round trip lost encoding", err)
			}
			_, err = Export(nodes, "egern")
			if err != nil {
				t.Fatal("Egern native conversion rejected source hint", err)
			}
		})
	}
	for _, query := range []string{"packetEncoding=invalid", "packetEncoding=xudp&packetEncoding=none"} {
		if _, err := Parse([]byte("vless://test-id@proxy.example:443?" + query)); err == nil {
			t.Fatal("accepted invalid encoding")
		}
	}
}

package backend

import (
	"context"
	"encoding/binary"
	"golang.org/x/net/dns/dnsmessage"
	"io"
	"net"
	"testing"
	"time"
)

func TestLookupIPv4RequestsOnlyA(t *testing.T) {
	queries := make(chan dnsmessage.Type, 4)
	resolver := &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			server.SetDeadline(time.Now().Add(time.Second))
			var size [2]byte
			if _, err := io.ReadFull(server, size[:]); err != nil {
				return
			}
			body := make([]byte, int(binary.BigEndian.Uint16(size[:])))
			if _, err := io.ReadFull(server, body); err != nil {
				return
			}
			var request dnsmessage.Message
			if request.Unpack(body) != nil || len(request.Questions) != 1 {
				return
			}
			q := request.Questions[0]
			queries <- q.Type
			response := dnsmessage.Message{Header: dnsmessage.Header{ID: request.ID, Response: true, RecursionAvailable: true}, Questions: request.Questions}
			if q.Type == dnsmessage.TypeA {
				response.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}}}
			}
			packet, err := response.Pack()
			if err != nil {
				return
			}
			binary.BigEndian.PutUint16(size[:], uint16(len(packet)))
			server.Write(append(size[:], packet...))
		}()
		return client, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := lookupIPv4(ctx, resolver, "example.invalid")
	if len(result) != 1 || result[0] != "127.0.0.1" {
		t.Fatalf("unexpected result %v", result)
	}
	if len(queries) == 0 {
		t.Fatal("no DNS query")
	}
	for len(queries) > 0 {
		if q := <-queries; q != dnsmessage.TypeA {
			t.Fatalf("unexpected DNS type %v", q)
		}
	}
}

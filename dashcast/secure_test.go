package main

import (
	"bytes"
	"crypto/rand"
	"io"
	"net"
	"testing"
)

// A device with the key gets through, and what each end writes - a picture far bigger than a record
// included - arrives whole at the other.
func TestTheHandshakeAndTheRecords(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	type got struct {
		c   *secureConn
		err error
	}
	server := make(chan got, 1)
	go func() {
		c, err := serverHandshake(b, "the key")
		server <- got{c, err}
	}()
	dev, err := clientHandshake(a, "the key")
	if err != nil {
		t.Fatal(err)
	}
	srv := <-server
	if srv.err != nil {
		t.Fatal(srv.err)
	}

	big := make([]byte, 3*recordMax+123)
	rand.Read(big)
	go func() { srv.c.Write(big) }()
	back := make([]byte, len(big))
	if _, err := io.ReadFull(dev, back); err != nil || !bytes.Equal(back, big) {
		t.Fatalf("server to device: %v, equal %v", err, bytes.Equal(back, big))
	}
	go func() { dev.Write([]byte("hello\n")) }()
	line := make([]byte, 6)
	if _, err := io.ReadFull(srv.c, line); err != nil || string(line) != "hello\n" {
		t.Fatalf("device to server: %q %v", line, err)
	}
}

// A device with another key fails the handshake, and sees nothing.
func TestAWrongKeyFails(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	errs := make(chan error, 1)
	go func() {
		_, err := serverHandshake(b, "the key")
		b.Close()
		errs <- err
	}()
	if _, err := clientHandshake(a, "another key"); err == nil {
		t.Error("the device got through with the wrong key")
	}
	if err := <-errs; err == nil {
		t.Error("the server let a wrong key through")
	}
}

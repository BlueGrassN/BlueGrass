package serviceipc

import (
	"path/filepath"
	"testing"
)

func TestPeerRequiresInstalledSignedBrokerAndMatchingInteractiveParent(t *testing.T) {
	dir := t.TempDir()
	good := Peer{Identity: Identity{SID: "S-1-5-21-123", PID: 42, Session: 1}, Image: filepath.Join(dir, "BlueGrass.Native.exe"), Signer: "trusted-publisher", ParentSID: "S-1-5-21-123", ParentSession: 1, ParentImage: filepath.Join(dir, "BlueGrass.exe"), ParentSigner: "trusted-publisher"}
	if err := ValidatePeer(good, dir, "trusted-publisher", false); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Peer){
		func(p *Peer) { p.Signer = "" }, func(p *Peer) { p.Signer = "other" },
		func(p *Peer) { p.Image = filepath.Join(dir, "subdir", "BlueGrass.Native.exe") },
		func(p *Peer) { p.Image = filepath.Join(dir, "BlueGrass.Service.exe") },
		func(p *Peer) { p.ParentImage = filepath.Join(dir, "other.exe") },
		func(p *Peer) { p.ParentSID = "S-1-5-18" }, func(p *Peer) { p.Session = 0 },
		func(p *Peer) { p.ParentSession = 2 }, func(p *Peer) { p.ParentSigner = "other" },
	} {
		p := good
		change(&p)
		if ValidatePeer(p, dir, "trusted-publisher", false) == nil {
			t.Fatal("untrusted caller accepted")
		}
	}
	if ValidatePeer(good, dir, "", false) == nil {
		t.Fatal("unsigned service accepted")
	}
}
func TestBrokerRequiresSystemServiceNotAPipeNameAlone(t *testing.T) {
	dir := t.TempDir()
	good := Peer{Identity: Identity{SID: "S-1-5-18", PID: 10, Session: 0}, Image: filepath.Join(dir, "BlueGrass.Service.exe"), Signer: "trusted"}
	if err := ValidatePeer(good, dir, "trusted", true); err != nil {
		t.Fatal(err)
	}
	good.SID = "S-1-5-21-123"
	if ValidatePeer(good, dir, "trusted", true) == nil {
		t.Fatal("user spoofed service accepted")
	}
}

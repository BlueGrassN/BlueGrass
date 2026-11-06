package serviceipc

import (
	"path/filepath"
	"testing"
)

func TestUpdateHelperIdentityDoesNotGrantNativeBrokerRole(t *testing.T) {
	root := t.TempDir()
	p := Peer{Identity: Identity{SID: "S-1-5-21-1-2-3-1001", PID: 99, Session: 1},
		Image: filepath.Join(root, "BlueGrass.Update.exe"), Signer: "trusted",
		ParentImage: filepath.Join(root, "BlueGrass.exe"), ParentSigner: "trusted", ParentSID: "S-1-5-21-1-2-3-1001", ParentSession: 1}
	if ValidateUpdatePeer(p, root, "trusted") != nil {
		t.Fatal("installed signed helper rejected")
	}
	if ValidatePeer(p, root, "trusted", false) == nil {
		t.Fatal("helper acquired broker role")
	}
	for _, change := range []func(*Peer){
		func(p *Peer) { p.Image = filepath.Join(root, "BlueGrass.Native.exe") },
		func(p *Peer) { p.Image = filepath.Join(root, "..", "BlueGrass.Update.exe") },
		func(p *Peer) { p.ParentImage = filepath.Join(root, "BlueGrass.Native.exe") },
		func(p *Peer) { p.ParentSigner = "untrusted" },
		func(p *Peer) { p.ParentSID = "S-1-5-18" },
		func(p *Peer) { p.ParentSession = 2 },
		func(p *Peer) { p.Signer = "untrusted" },
		func(p *Peer) { p.Session = 0 },
	} {
		bad := p
		change(&bad)
		if ValidateUpdatePeer(bad, root, "trusted") == nil {
			t.Fatal("forged updater identity accepted", bad.Image)
		}
	}
}

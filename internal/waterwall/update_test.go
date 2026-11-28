package waterwall

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestWaterWallUpdateToken(t *testing.T) {
	tag, err := UpdateTag("ww1.v1.6.0")
	if err != nil || tag != Version || UpdateCommand(tag) != "sudo backpack waterwall update 'ww1.v1.6.0'" {
		t.Fatalf("unexpected token result %q, %v", tag, err)
	}
	for _, token := range []string{"v1.6.0", "ww1.v1.6.0/../../bin", "ww1.v1.6.1-rc1", "ww1." + Protocol17Version} {
		if _, err := UpdateTag(token); err == nil {
			t.Errorf("accepted invalid token %q", token)
		}
	}
}

func TestWaterWallReleaseAssetDigest(t *testing.T) {
	assetPath := releasePath + "/download/" + Version + "/"
	markup := ""
	for _, b := range []build{standard, compatible} {
		markup += fmt.Sprintf(`<li><a href="%s%s.zip">Download</a><clipboard-copy value="sha256:%s"></clipboard-copy></li>`,
			assetPath, b.name, b.archiveHash)
	}
	for _, b := range []build{standard, compatible} {
		digest, err := assetDigest(strings.NewReader(markup), assetPath+b.name+".zip")
		if err != nil || digest != b.archiveHash {
			t.Errorf("%s: got %q, %v", b.name, digest, err)
		}
	}
	if _, err := assetDigest(strings.NewReader(markup), assetPath+"Waterwall-linux-arm64.zip"); err == nil {
		t.Error("accepted a digest from another asset")
	}
}

func TestWaterWallUpdateRejectsInvalidExecutable(t *testing.T) {
	var buffer bytes.Buffer
	z := zip.NewWriter(&buffer)
	file, err := z.Create("Waterwall")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("not an elf executable")); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	b := build{version: "v1.99.0", name: compatible.name, archiveHash: compatible.archiveHash}
	if _, err := b.unpack(buffer.Bytes()); err == nil || !strings.Contains(err.Error(), "archive checksum") {
		t.Fatalf("wrong archive digest: %v", err)
	}
	b.archiveHash = fmt.Sprintf("%x", sha256.Sum256(buffer.Bytes()))
	if _, err := b.unpack(buffer.Bytes()); err == nil || !strings.Contains(err.Error(), "invalid WaterWall executable") {
		t.Fatalf("invalid executable: %v", err)
	}
}

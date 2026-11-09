package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	yzip "github.com/yeka/zip"
)

// promptSeq: a jelszókérő helyettesítője – sorban adja a válaszokat.
func promptSeq(t *testing.T, answers ...string) *[]bool {
	var asked []bool
	passwordPrompt = func(name string, wrong bool) (string, bool) {
		asked = append(asked, wrong)
		if len(answers) == 0 {
			return "", false
		}
		a := answers[0]
		answers = answers[1:]
		return a, true
	}
	t.Cleanup(func() { passwordPrompt = nil })
	return &asked
}

func TestEncryptedZip(t *testing.T) {
	for _, m := range []yzip.EncryptionMethod{yzip.StandardEncryption, yzip.AES256Encryption} {
		dir := t.TempDir()
		p := filepath.Join(dir, "titkos.zip")
		f, _ := os.Create(p)
		zw := yzip.NewWriter(f)
		w, err := zw.Encrypt("titok.txt", "Jelszó123", m)
		must(t, err)
		w.Write([]byte(strings.Repeat("titkos tartalom ", 1000)))
		w2, _ := zw.Create("nyilt.txt")
		w2.Write([]byte("nyílt"))
		zw.Close()
		f.Close()

		a, err := openArchive(p)
		must(t, err)
		root := listOf(t, a, a.Root())
		if len(root) != 2 {
			t.Fatalf("lista: %v", names(root))
		}
		asked := promptSeq(t, "rossz", "Jelszó123")
		out := t.TempDir()
		pr := &testProg{}
		must(t, a.Extract(root, out, pr))
		if len(pr.errs) > 0 {
			t.Fatal(pr.errs)
		}
		if b, _ := os.ReadFile(filepath.Join(out, "titok.txt")); !strings.HasPrefix(string(b), "titkos tartalom") {
			t.Errorf("%v: tartalom: %q", m, string(b[:min(len(b), 20)]))
		}
		if len(*asked) != 2 || (*asked)[0] || !(*asked)[1] {
			t.Errorf("%v: kérdések (rossz-e): %v", m, *asked)
		}
		// a jelszót megjegyezte: újra nem kérdez
		asked = promptSeq(t)
		must(t, a.Extract(root, t.TempDir(), &testProg{}))
		if len(*asked) != 0 {
			t.Error("másodszor is kérdezett")
		}
		// Mégse: nem bont ki semmit, errCancel
		forgetArchivePassword(p)
		promptSeq(t)
		out2 := t.TempDir()
		if err := a.Extract(root, out2, &testProg{}); err != errCancel {
			t.Errorf("Mégse után: %v", err)
		}
		if _, err := os.Stat(filepath.Join(out2, "titok.txt")); err == nil {
			t.Error("Mégse után is kibontotta")
		}
		// írás titkosított archívumba: a meglévő titkos bejegyzés épen marad
		src := filepath.Join(dir, "uj.txt")
		os.WriteFile(src, []byte("új"), 0o644)
		must(t, a.AddLocal([]string{src}, a.Root(), &testProg{}))
		setArchivePassword(p, "Jelszó123")
		out3 := t.TempDir()
		must(t, a.Extract(listOf(t, a, a.Root()), out3, &testProg{}))
		if b, _ := os.ReadFile(filepath.Join(out3, "titok.txt")); !strings.HasPrefix(string(b), "titkos") {
			t.Error("hozzáírás után a titkos bejegyzés sérült")
		}
		forgetArchivePassword(p)
	}
}

func TestEncrypted7z(t *testing.T) {
	mc, _ := exec.Command("go", "env", "GOMODCACHE").Output()
	p := filepath.Join(strings.TrimSpace(string(mc)), "github.com/bodgit/sevenzip@v1.5.2/testdata/aes7z.7z")
	if _, err := os.Stat(p); err != nil {
		t.Skip("nincs mintaadat")
	}
	forgetArchivePassword(p)
	asked := promptSeq(t, "rossz", "password")
	a, err := openArchiveWorker(p)
	must(t, err)
	root := listOf(t, a, a.Root())
	out := t.TempDir()
	pr := &testProg{}
	must(t, a.Extract(root, out, pr))
	if len(pr.errs) > 0 {
		t.Fatal(pr.errs)
	}
	if len(readTree(t, out)) == 0 {
		t.Error("semmi nem bontódott ki")
	}
	t.Logf("fejléc titkosítva: %v, kérdések: %v", sevenHeaderEncrypted(p), *asked)
	if len(*asked) != 2 || !(*asked)[1] {
		t.Errorf("kérdések: %v", *asked)
	}
	forgetArchivePassword(p)
}

func TestPackZipEncrypted(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"mappa/ékezetes név.txt": "titok", "mappa/al/b.txt": strings.Repeat("b", 50000)})
	dest := filepath.Join(dir, "ki.zip")
	must(t, packZipEncrypted([]string{filepath.Join(dir, "mappa")}, dest, "Jó jelszó", &testProg{}))
	if err := packZipEncrypted([]string{filepath.Join(dir, "mappa")}, dest, "x", &testProg{}); err == nil {
		t.Error("meglévőt felülírta")
	}
	a, err := openArchive(dest)
	must(t, err)
	promptSeq(t, "Jó jelszó")
	out := t.TempDir()
	must(t, a.Extract(listOf(t, a, a.Root()), out, &testProg{}))
	tr := readTree(t, out)
	if tr["mappa/ékezetes név.txt"] != "titok" || len(tr["mappa/al/b.txt"]) != 50000 {
		t.Errorf("tartalom: %v", keysOf(tr))
	}
	forgetArchivePassword(dest)
}

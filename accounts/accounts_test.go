package accounts_test

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/dgnorton/faxme/accounts"
)

func Test_ReadFile(t *testing.T) {
	dir := mustTempDir()
	defer os.RemoveAll(dir)

	cfgData := `
[
	{
		"fax_number": "12223334444",
		"contacts": ["14443332222", "19998887777"]
	},
	{
		"fax_number": "14443332222",
		"contacts": ["15556667777", "17776665555"]
	}
]`

	cfgFile := mustWriteTempFile(dir, "faxme.*.json", cfgData)

	acnts, err := accounts.ReadFile(cfgFile)
	if err != nil {
		t.Fatal(err)
	}

	acnt := acnts.Find("12223334444")
	if len(acnt.Contacts) != 2 {
		t.Fatalf("exp: 2, got: %d", len(acnt.Contacts))
	} else if acnt.Contacts[0] != "14443332222" {
		t.Fatalf("exp: \"14443332222\", got: %q", acnt.Contacts[0])
	} else if acnt.Contacts[1] != "19998887777" {
		t.Fatalf("exp: \"19998887777\", got: %q", acnt.Contacts[1])
	}

	acnt = acnts.Find("14443332222")
	if len(acnt.Contacts) != 2 {
		t.Fatalf("exp: 2, got: %d", len(acnt.Contacts))
	} else if acnt.Contacts[0] != "15556667777" {
		t.Fatalf("exp: \"15556667777\", got: %q", acnt.Contacts[0])
	} else if acnt.Contacts[1] != "17776665555" {
		t.Fatalf("exp: \"17776665555\", got: %q", acnt.Contacts[1])
	}
}

func mustTempDir() string {
	dir, err := ioutil.TempDir("", "faxme_")
	if err != nil {
		panic(err)
	}
	return dir
}

func mustWriteTempFile(dir string, pattern, s string) string {
	f, err := ioutil.TempFile(dir, pattern)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	if _, err := f.Write([]byte(s)); err != nil {
		panic(err)
	}

	return f.Name()
}

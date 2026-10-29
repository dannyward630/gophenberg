// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadRecordReturnsAnEmptyRecordBeforeAnyRunWroteOne(t *testing.T) {
	t.Parallel()

	record, err := readRecord(serverWith(t, nil))

	if err != nil || !reflect.DeepEqual(record, Record{}) {
		t.Errorf("readRecord() = %+v, %v, want an empty record", record, err)
	}
}

func TestReadRecordReadsWhatAnEarlierRunWroteAndSkipsWhatItDoesNotKnow(t *testing.T) {
	t.Parallel()

	machine := serverWith(t, map[string]string{recordPath: `{"installer": "0.20.0", "steps": {"packages": "ended", ` +
		`"user": "started"}, "installed": ["postgresql", "caddy"], "domain": "example.com", "a later field": true}`})

	record, err := readRecord(machine)

	want := Record{
		Installer: "0.20.0", Steps: map[string]string{"packages": "ended", "user": "started"},
		Installed: []string{"postgresql", "caddy"}, Domain: "example.com",
	}
	if err != nil || !reflect.DeepEqual(record, want) {
		t.Errorf("readRecord() = %+v, %v, want %+v", record, err, want)
	}
}

func TestReadRecordReadsTheChecksumOfTheCaddyfileTheFirstRunWrote(t *testing.T) {
	t.Parallel()

	machine := serverWith(t, map[string]string{recordPath: `{"installer": "0.20.0", "caddyfile": "` + abcSum + `"}`})

	record, err := readRecord(machine)

	if err != nil || record.Caddyfile != abcSum {
		t.Errorf("readRecord() = %+v, %v, want the Caddyfile's checksum %s", record, err, abcSum)
	}
}

// abcSum is the SHA-256 of abc in hex, the first example of FIPS 180-2.
const abcSum = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"

func TestCaddyfileSumIsTheSHA256OfTheTextInHex(t *testing.T) {
	t.Parallel()

	if got := caddyfileSum("abc"); got != abcSum {
		t.Errorf("caddyfileSum(abc) = %s, want %s", got, abcSum)
	}
}

func TestStartedReportsAStepARunStartedAndDidNotEnd(t *testing.T) {
	t.Parallel()

	record := Record{Steps: map[string]string{"packages": "started", "user": "ended"}}

	for id, want := range map[string]bool{"packages": true, "user": false, "database": false} {
		if got := record.started(id); got != want {
			t.Errorf("started(%s) = %t, want %t", id, got, want)
		}
	}
}

func TestReadRecordRefusesARecordItCannotRead(t *testing.T) {
	t.Parallel()

	machine := serverWith(t, nil)
	if err := os.MkdirAll(filepath.Join(machine.Root, recordPath), 0o700); err != nil {
		t.Fatalf("making a folder where the record goes: %v", err)
	}

	_, err := readRecord(machine)

	if got := fmt.Sprint(err); !strings.HasPrefix(got, "installer: read ") || !strings.HasSuffix(got, recordPath+
		": is a directory") {
		t.Errorf("readRecord() = %v, want the record refused as a folder", err)
	}
}

func TestReadRecordRefusesARecordThatIsNotJSON(t *testing.T) {
	t.Parallel()

	for _, body := range []string{`{"installer": "0.20.0"`, ""} {
		machine := serverWith(t, map[string]string{recordPath: body})

		_, err := readRecord(machine)

		wantRefused(t, err, "installer: /var/lib/gophenberg-installer/install.json is not an install record: "+
			"unexpected end of JSON input")
	}
}

func TestReadRecordRefusesARecordThatNamesNoInstallerVersion(t *testing.T) {
	t.Parallel()

	for body, named := range map[string]string{`{}`: "", `{"installer": "v0.20.0"}`: "v0.20.0", `null`: ""} {
		machine := serverWith(t, map[string]string{recordPath: body})

		_, err := readRecord(machine)

		wantRefused(t, err, fmt.Sprintf("installer: /var/lib/gophenberg-installer/install.json names the installer "+
			"%q, which is not a version", named))
	}
}

func TestAllowsTakesAnInstallerAsNewAsTheOneTheRecordNamesOrNewer(t *testing.T) {
	t.Parallel()

	for _, running := range []string{"0.20.0", "0.20.1", "1.0.0"} {
		if err := (Record{Installer: "0.20.0"}).allows(running); err != nil {
			t.Errorf("allows(%s) over a record of 0.20.0 = %v, want nil", running, err)
		}
	}
	if err := (Record{}).allows("0.20.0"); err != nil {
		t.Errorf("allows() over an empty record = %v, want nil", err)
	}
}

func TestAllowsWebServerRefusesARunWithoutCaddyWhileGophenbergsCaddySiteIsThere(t *testing.T) {
	t.Parallel()

	machine := serverWith(t, map[string]string{caddySitePath: wantedCaddySite})
	machine.Lstat = os.Lstat

	err := allowsWebServer(machine, Settings{WebServer: "none"}, envOf(nil))

	wantRefused(t, err, fmt.Sprintf(webServerMove, "caddy", "none"))
}

func TestAllowsWebServerTakesARunWithCaddyOrOneWithoutGophenbergsCaddySiteWhateverTheRecordOwns(t *testing.T) {
	t.Parallel()

	sited := serverWith(t, map[string]string{caddySitePath: wantedCaddySite})
	bare := serverWith(t, map[string]string{caddyfilePath: packageCaddyfile})
	for machine, webServer := range map[*Machine]string{&sited: "caddy", &bare: "none"} {
		machine.Lstat = os.Lstat

		err := allowsWebServer(*machine, Settings{WebServer: webServer}, envOf(nil))

		if err != nil {
			t.Errorf("allowsWebServer(%s) = %v, want nil", webServer, err)
		}
	}
}

func TestAllowsRefusesAnInstallerOlderThanTheOneTheRecordNames(t *testing.T) {
	t.Parallel()

	for running, recorded := range map[string]string{"0.20.0": "0.21.0", "0.21.0-rc.1": "0.21.0"} {
		err := Record{Installer: recorded}.allows(running)

		wantRefused(t, err, "this installer is "+running+", and this server was set up by the "+recorded+
			" installer, so run the install.sh line again")
	}
}

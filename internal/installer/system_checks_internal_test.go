// SPDX-License-Identifier: Apache-2.0

package installer

import (
	"errors"
	"fmt"
	"maps"
	"testing"
)

// debianRelease returns the os-release of Debian major, called codename.
func debianRelease(major int, codename string) string {
	return fmt.Sprintf("PRETTY_NAME=\"Debian GNU/Linux %d (%s)\"\nNAME=\"Debian GNU/Linux\"\nVERSION_ID=\"%d\"\n"+
		"VERSION=\"%d (%s)\"\nVERSION_CODENAME=%s\nID=debian\n", major, codename, major, major, codename, codename)
}

func TestOsReleaseReadsEachSettingWithoutTheQuotesAroundIt(t *testing.T) {
	t.Parallel()

	for body, want := range map[string]map[string]string{
		ubuntuRelease: {
			"PRETTY_NAME": "Ubuntu 24.04.3 LTS", "NAME": "Ubuntu", "VERSION_ID": "24.04",
			"VERSION": "24.04.3 LTS (Noble Numbat)", "VERSION_CODENAME": "noble", "ID": "ubuntu", "ID_LIKE": "debian",
		},
		"ID=debian\nVERSION_ID='13'\n":             {"ID": "debian", "VERSION_ID": "13"},
		"  ID=debian  \n\n# a line of notes\n":     {"ID": "debian"},
		"ID=\"unclosed\nNAME='\nVERSION_ID=\"\"\n": {"ID": `"unclosed`, "NAME": "'", "VERSION_ID": ""},
		"ID=\"mixed'\nNAME=a=b\n":                  {"ID": `"mixed'`, "NAME": "a=b"},
		"":                                         {},
	} {
		if got := osRelease([]byte(body)); !maps.Equal(got, want) {
			t.Errorf("osRelease(%q) = %q, want %q", body, got, want)
		}
	}
}

func TestSystemdMajorReadsTheFirstLineOfSystemctlVersion(t *testing.T) {
	t.Parallel()

	for printed, want := range map[string]int{
		"systemd 255 (255.4-1ubuntu8.6)\n+PAM +AUDIT\n": 255,
		"systemd 252 (252.30-1~deb12u2)\n":              252,
		"systemd 257 (257.8-1~deb13u2)":                 257,
		"systemd 251":                                   251,
	} {
		if got, err := systemdMajor(printed); err != nil || got != want {
			t.Errorf("systemdMajor(%q) = %d, %v, want %d", printed, got, err, want)
		}
	}
}

func TestSystemdMajorRefusesAnyOtherFirstLine(t *testing.T) {
	t.Parallel()

	for printed, first := range map[string]string{
		"": "", "elogind 252\n+PAM\n": "elogind 252", "systemd version 255\n": "systemd version 255",
		" systemd 255\n": " systemd 255", "+PAM\nsystemd 255\n": "+PAM",
	} {
		_, err := systemdMajor(printed)

		wantRefused(t, err, fmt.Sprintf("systemctl --version printed %q, which names no systemd version", first))
	}
}

func TestSystemdMajorRefusesAMajorTooLargeToCount(t *testing.T) {
	t.Parallel()

	_, err := systemdMajor("systemd 99999999999999999999 (99)\n")

	wantRefused(t, err, `strconv.Atoi: parsing "99999999999999999999": value out of range`)
}

func TestMemTotalMBReadsTheMemTotalLineInMegabytes(t *testing.T) {
	t.Parallel()

	for meminfo, want := range map[string]int{
		meminfoOf(2014532):                        1967,
		"MemFree:  1 kB\nMemTotal:\t1024 kB\n":    1,
		"MemTotal:        1023 kB\nMemFree: 1 kB": 0,
	} {
		if got, err := memTotalMB([]byte(meminfo)); err != nil || got != want {
			t.Errorf("memTotalMB(%q) = %d, %v, want %d", meminfo, got, err, want)
		}
	}
}

func TestMemTotalMBRefusesAReportWithoutAMemTotalLineInKB(t *testing.T) {
	t.Parallel()

	for _, meminfo := range []string{
		"", "MemFree:          812345 kB\n", "MemTotal:        2014532 MB\n", "MemTotal:2014532 kB\n",
		"  MemTotal:        2014532 kB\n", "MemTotal:        2014532 kB extra\n",
	} {
		_, err := memTotalMB([]byte(meminfo))

		wantRefused(t, err, "/proc/meminfo has no MemTotal line in kB")
	}
}

func TestMemTotalMBRefusesAMemoryTooLargeToCount(t *testing.T) {
	t.Parallel()

	_, err := memTotalMB([]byte("MemTotal:   99999999999999999999 kB\n"))

	wantRefused(t, err, `strconv.Atoi: parsing "99999999999999999999": value out of range`)
}

func TestTheMachineChecksNoteThatDebianIsNotYetTestedInCI(t *testing.T) {
	t.Parallel()

	for major, codename := range map[int]string{12: "bookworm", 13: "trixie"} {
		server := newFakeServer(t)
		server.write("/etc/os-release", debianRelease(major, codename))

		err := server.check().run(t.Context())

		server.wantPassed(err, fmt.Sprintf("Debian GNU/Linux %d (%s) is not yet tested in Gophenberg's CI, which "+
			"tests Ubuntu 24.04\n", major, codename))
	}
}

func TestTheMachineChecksStopOnASystemTheyDoNotSetUp(t *testing.T) {
	t.Parallel()

	for release, named := range map[string]string{
		"PRETTY_NAME=\"Ubuntu 22.04.5 LTS\"\nVERSION_ID=\"22.04\"\nID=ubuntu\n": "Ubuntu 22.04.5 LTS",
		"PRETTY_NAME=\"Fedora Linux 41 (Server Edition)\"\nVERSION_ID=41\nID=fedora\n": "Fedora " +
			"Linux 41 (Server Edition)",
		"PRETTY_NAME=\"Debian GNU/Linux forky/sid\"\nID=debian\n": "Debian GNU/Linux forky/sid",
		debianRelease(11, "bullseye"):                             "Debian GNU/Linux 11 (bullseye)",
		"ID=alpine\nVERSION_ID=3.20.3\n":                          "alpine 3.20.3",
		"ID=ubuntu\n":                                             "ubuntu",
		"NAME=Linux\n":                                            "a system /etc/os-release does not name",
	} {
		server := newFakeServer(t)
		server.write("/etc/os-release", release)

		err := server.check().run(t.Context())

		wantRefused(t, err, "this server runs "+named+", and server:install sets up Ubuntu 24.04, Debian 12 and "+
			"Debian 13 only, so nothing was changed")
	}
}

func TestTheMachineChecksStopOnAServerWithNoOsRelease(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.remove("/etc/os-release")

	err := server.check().run(t.Context())

	wantRefused(t, err, "this server runs a system /etc/os-release does not name, and server:install sets up "+
		"Ubuntu 24.04, Debian 12 and Debian 13 only, so nothing was changed")
}

func TestTheMachineChecksRefuseAnOsReleaseTheyCannotRead(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.remove("/etc/os-release")
	server.folder("/etc/os-release")

	err := server.check().run(t.Context())

	wantRefused(t, err, "installer: read "+server.at("/etc/os-release")+": is a directory")
}

func TestTheMachineChecksStopWhereSystemdDoesNotRunTheServer(t *testing.T) {
	t.Parallel()

	missing := newFakeServer(t)
	missing.remove("/run/systemd/system")
	file := newFakeServer(t)
	file.remove("/run/systemd/system")
	file.write("/run/systemd/system", "")
	link := newFakeServer(t)
	link.remove("/run/systemd/system")
	link.link("/run/systemd/system", "/opt")

	for _, server := range []*fakeServer{missing, file, link} {
		err := server.check().run(t.Context())

		wantRefused(t, err, "systemd does not run this server, since /run/systemd/system is missing, and the site "+
			"runs as a systemd service, so nothing was changed")
	}
}

func TestTheMachineChecksTakeSystemd251AndKeepItsMajor(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.answers["systemctl"] = answer{printed: "systemd 251 (251.4-3)\n"}
	check := server.check()

	err := check.run(t.Context())

	server.wantPassed(err, "")
	if check.found.systemd != 251 {
		t.Errorf("the checks found systemd %d, want 251", check.found.systemd)
	}
}

func TestTheMachineChecksStopOnASystemdOlderThan251(t *testing.T) {
	t.Parallel()

	server := newFakeServer(t)
	server.answers["systemctl"] = answer{printed: "systemd 250 (250.3-2)\n"}

	err := server.check().run(t.Context())

	wantRefused(t, err, "this server runs systemd 250, and the site's units need systemd 251 or newer, so nothing "+
		"was changed")
}

func TestTheMachineChecksStopWhenTheyCannotTellWhichSystemdRuns(t *testing.T) {
	t.Parallel()

	failed := answer{err: errors.New("installer: run systemctl: exit status 1")}
	other := answer{printed: "elogind 252\n"}
	for reply, why := range map[answer]string{
		failed: "installer: run systemctl: exit status 1",
		other:  `systemctl --version printed "elogind 252", which names no systemd version`,
	} {
		server := newFakeServer(t)
		server.answers["systemctl"] = reply

		err := server.check().run(t.Context())

		wantRefused(t, err, "cannot tell which systemd this server runs: "+why+", so nothing was changed")
	}
}

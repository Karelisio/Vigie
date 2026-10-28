package vulns

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		eco, a, b string
		want      int
	}{
		// dpkg : époque, « ~ » avant tout, révision Debian.
		{"Debian:12", "3.0.11-1~deb12u2", "3.0.15-1~deb12u1", -1},
		{"Debian:12", "3.0.15-1~deb12u1", "3.0.15-1", -1},
		{"Debian:12", "1:2.0-1", "9.9-1", 1},
		{"Debian:12", "2.36-9+deb12u4", "2.36-9+deb12u10", -1},
		{"Debian:12", "1.2.3-1", "1.2.3-1", 0},
		{"Debian:12", "1.0~rc1-1", "1.0-1", -1},
		{"Ubuntu:22.04:LTS", "2.35-0ubuntu3.6", "2.35-0ubuntu3.10", -1},
		{"Debian:12", "7.88.1-10+deb12u5", "7.88.1-10+deb12u12", -1},
		// apk : suffixes de préversion et révision -r.
		{"Alpine:v3.20", "1.36.1-r29", "1.36.1-r30", -1},
		{"Alpine:v3.20", "3.3.2-r0", "3.3.10-r0", -1},
		{"Alpine:v3.20", "1.2.3_rc1-r0", "1.2.3-r0", -1},
		{"Alpine:v3.20", "1.2.3_p1-r0", "1.2.3-r5", 1},
		{"Alpine:v3.20", "1.2a-r0", "1.2-r0", 1},
		{"Alpine:v3.20", "8.9.1-r0", "8.9.1-r0", 0},
		// SemVer : préversions avant la version finale.
		{"npm", "4.17.20", "4.17.21", -1},
		{"npm", "10.0.0", "9.99.99", 1},
		{"npm", "1.0.0-beta.2", "1.0.0", -1},
		{"npm", "1.0.0-alpha.1", "1.0.0-alpha.beta", -1},
		{"npm", "1.0.0-rc.10", "1.0.0-rc.9", 1},
		{"npm", "2.0.0+build.5", "2.0.0", 0},
	}
	for _, c := range cases {
		if got := compareVersions(c.eco, c.a, c.b); got != c.want {
			t.Errorf("%s : %s ? %s = %d, attendu %d", c.eco, c.a, c.b, got, c.want)
		}
		if got := compareVersions(c.eco, c.b, c.a); got != -c.want {
			t.Errorf("%s : %s ? %s = %d, attendu %d (symétrie)", c.eco, c.b, c.a, got, -c.want)
		}
	}
}

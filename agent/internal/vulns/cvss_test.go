package vulns

import "testing"

// Vecteurs de référence : scores publiés (NVD, GitHub, VulDB) et exemples de la spécification FIRST.
func TestCVSS3Scores(t *testing.T) {
	for vec, want := range map[string]float64{
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H": 9.8,
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:C/C:H/I:H/A:H": 10,
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:R/S:C/C:L/I:L/A:N": 6.1,
		"CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:C/C:L/I:L/A:N": 6.4,
		"CVSS:3.1/AV:L/AC:L/PR:L/UI:N/S:U/C:H/I:H/A:H": 7.8,
		"CVSS:3.1/AV:L/AC:L/PR:N/UI:R/S:U/C:H/I:H/A:H": 7.8,
		"CVSS:3.1/AV:L/AC:L/PR:L/UI:N/S:U/C:H/I:N/A:N": 5.5,
		"CVSS:3.1/AV:N/AC:H/PR:N/UI:N/S:U/C:H/I:N/A:N": 5.9,
		"CVSS:3.1/AV:N/AC:H/PR:N/UI:N/S:U/C:H/I:H/A:H": 8.1,
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H": 7.5,
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:L/I:N/A:N": 5.3,
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:L/I:L/A:N": 6.5,
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:R/S:U/C:H/I:H/A:H": 8.8,
		"CVSS:3.1/AV:N/AC:L/PR:H/UI:N/S:U/C:H/I:H/A:H": 7.2,
		"CVSS:3.1/AV:P/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H": 6.8,
		"CVSS:3.1/AV:A/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H": 8.8,
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:N": 0,
		"CVSS:3.0/AV:N/AC:L/PR:L/UI:N/S:U/C:H/I:H/A:H": 8.8,
		// Métriques temporelles ignorées : score de base.
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H/E:P/RL:O/RC:C": 9.8,
	} {
		got, err := cvss3Score(vec)
		if err != nil || got != want {
			t.Errorf("%s : %v (%v), attendu %v", vec, got, err, want)
		}
	}
	for _, bad := range []string{
		"", "CVSS:2.0/AV:N", "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:X/C:H/I:H/A:H", "CVSS:3.1/AV:N/AC:L",
		"CVSS:3.1/AV:N/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", "AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",
	} {
		if _, err := cvss3Score(bad); err == nil {
			t.Errorf("%q : erreur attendue", bad)
		}
	}
}

func TestCVSS4Scores(t *testing.T) {
	for vec, want := range map[string]float64{
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N": 9.3,
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:H/SI:H/SA:H": 10,
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:L/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N": 8.7,
		"CVSS:4.0/AV:L/AC:L/AT:N/PR:L/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N": 8.5,
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:N/VI:N/VA:H/SC:N/SI:N/SA:N": 8.7,
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:L/VI:N/VA:N/SC:N/SI:N/SA:N": 6.9,
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:L/VI:L/VA:L/SC:N/SI:N/SA:N": 6.9,
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:L/UI:N/VC:L/VI:L/VA:L/SC:N/SI:N/SA:N": 5.3,
		"CVSS:4.0/AV:L/AC:L/AT:N/PR:L/UI:N/VC:L/VI:L/VA:L/SC:N/SI:N/SA:N": 4.8,
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:A/VC:N/VI:N/VA:N/SC:L/SI:L/SA:N": 5.1,
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:P/VC:N/VI:N/VA:N/SC:L/SI:L/SA:N": 5.3,
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:N/VI:N/VA:N/SC:N/SI:N/SA:N": 0,
		// Ordre des métriques libre, « X » explicite.
		"CVSS:4.0/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N/AV:N/AC:L/AT:N/PR:N/UI:N/E:X": 9.3,
	} {
		got, err := cvss4Score(vec)
		if err != nil || got != want {
			t.Errorf("%s : %v (%v), attendu %v", vec, got, err, want)
		}
	}
	for _, bad := range []string{
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N",           // SA manquant
		"CVSS:4.0/AV:Z/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N",      // valeur inconnue
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N/ZZ:1", // métrique inconnue
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",
	} {
		if _, err := cvss4Score(bad); err == nil {
			t.Errorf("%q : erreur attendue", bad)
		}
	}
}

// Tous les macro-vecteurs restent dans [0, 10] et chaque score est arrondi au dixième.
func TestCVSS4Bounds(t *testing.T) {
	av, ac, at, pr, ui := []string{"N", "A", "L", "P"}, []string{"L", "H"}, []string{"N", "P"}, []string{"N", "L", "H"}, []string{"N", "P", "A"}
	imp := []string{"H", "L", "N"}
	n := 0
	for _, a := range av {
		for _, b := range ac {
			for _, c := range at {
				for _, d := range pr {
					for _, e := range ui {
						for _, vc := range imp {
							for _, sc := range imp {
								vec := "CVSS:4.0/AV:" + a + "/AC:" + b + "/AT:" + c + "/PR:" + d + "/UI:" + e +
									"/VC:" + vc + "/VI:L/VA:N/SC:" + sc + "/SI:N/SA:L/E:P"
								s, err := cvss4Score(vec)
								if err != nil || s < 0 || s > 10 || s*10 != float64(int(s*10+0.5)) {
									t.Fatalf("%s : %v (%v)", vec, s, err)
								}
								n++
							}
						}
					}
				}
			}
		}
	}
	if n != 4*2*2*3*3*9 {
		t.Fatal(n)
	}
}

func TestSeverityOf(t *testing.T) {
	cases := []struct {
		list  []osvSeverity
		level string
		score float64
	}{
		{[]osvSeverity{{"CVSS_V3", "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}}, "critical", 9.8},
		// v3 préféré à v4 quand les deux sont fournis.
		{[]osvSeverity{{"CVSS_V4", "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:L/VI:N/VA:N/SC:N/SI:N/SA:N"}, {"CVSS_V3", "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H"}}, "high", 7.5},
		{[]osvSeverity{{"CVSS_V4", "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:L/VI:N/VA:N/SC:N/SI:N/SA:N"}}, "medium", 6.9},
		{[]osvSeverity{{"CVSS_V3", "n'importe quoi"}, {"Ubuntu", "high"}}, "high", 0},
		{[]osvSeverity{{"CVSS_V2", "AV:N/AC:L/Au:N/C:P/I:P/A:P"}}, "unknown", 0},
		{nil, "unknown", 0},
		{[]osvSeverity{{"CVSS_V3", "CVSS:3.1/AV:L/AC:H/PR:H/UI:R/S:U/C:L/I:N/A:N"}}, "low", 1.8},
	}
	for i, c := range cases {
		lvl, score := severityOf(c.list)
		if lvl != c.level || score != c.score {
			t.Errorf("cas %d : %s %v, attendu %s %v", i, lvl, score, c.level, c.score)
		}
	}
}

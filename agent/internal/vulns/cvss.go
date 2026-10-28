package vulns

// Score de base CVSS v3.x et v4.0 calculé à partir du vecteur publié dans les fiches OSV.
// v3 : formules de la spécification FIRST (arrondi « Roundup » de la 3.1).
// v4 : algorithme des macro-vecteurs de la spécification FIRST CVSS v4.0, d'après le calculateur
// de référence (FIRST.ORG, Red Hat et contributeurs, licence BSD à 2 clauses).

import (
	"errors"
	"math"
	"strings"
)

var errVector = errors.New("vecteur CVSS invalide")

// parseVector lit « CVSS:x.y/AV:N/… » en métriques ; une métrique en double est refusée.
func parseVector(vec, prefix string) (map[string]string, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(vec), prefix)
	if !ok {
		return nil, errVector
	}
	m := map[string]string{}
	for _, f := range strings.Split(rest, "/") {
		k, v, ok := strings.Cut(f, ":")
		if !ok || k == "" || v == "" {
			return nil, errVector
		}
		if _, dup := m[k]; dup {
			return nil, errVector
		}
		m[k] = v
	}
	return m, nil
}

// cvss3Score calcule le score de base d'un vecteur CVSS v3.0 ou v3.1.
func cvss3Score(vec string) (float64, error) {
	prefix := "CVSS:3.1/"
	roundup := roundup31
	if strings.HasPrefix(vec, "CVSS:3.0/") {
		prefix, roundup = "CVSS:3.0/", func(x float64) float64 { return math.Ceil(x*10-1e-9) / 10 }
	}
	m, err := parseVector(vec, prefix)
	if err != nil {
		return 0, err
	}
	pick := func(k string, vals map[string]float64) (float64, error) {
		v, ok := vals[m[k]]
		if !ok {
			return 0, errVector
		}
		return v, nil
	}
	changed := m["S"] == "C"
	if m["S"] != "U" && !changed {
		return 0, errVector
	}
	cia := map[string]float64{"H": 0.56, "L": 0.22, "N": 0}
	av, e1 := pick("AV", map[string]float64{"N": 0.85, "A": 0.62, "L": 0.55, "P": 0.2})
	ac, e2 := pick("AC", map[string]float64{"L": 0.77, "H": 0.44})
	prVals := map[string]float64{"N": 0.85, "L": 0.62, "H": 0.27}
	if changed {
		prVals = map[string]float64{"N": 0.85, "L": 0.68, "H": 0.5}
	}
	pr, e3 := pick("PR", prVals)
	ui, e4 := pick("UI", map[string]float64{"N": 0.85, "R": 0.62})
	c, e5 := pick("C", cia)
	i, e6 := pick("I", cia)
	a, e7 := pick("A", cia)
	if err := errors.Join(e1, e2, e3, e4, e5, e6, e7); err != nil {
		return 0, errVector
	}
	iss := 1 - (1-c)*(1-i)*(1-a)
	impact := 6.42 * iss
	if changed {
		impact = 7.52*(iss-0.029) - 3.25*math.Pow(iss-0.02, 15)
	}
	if impact <= 0 {
		return 0, nil
	}
	expl := 8.22 * av * ac * pr * ui
	if changed {
		return roundup(math.Min(1.08*(impact+expl), 10)), nil
	}
	return roundup(math.Min(impact+expl, 10)), nil
}

// roundup31 : plus petit nombre à une décimale supérieur ou égal (CVSS 3.1, sans erreur d'arrondi flottant).
func roundup31(x float64) float64 {
	n := int64(math.Round(x * 100000))
	if n%10000 == 0 {
		return float64(n) / 100000
	}
	return (math.Floor(float64(n)/10000) + 1) / 10
}

// ---- CVSS v4.0 ----

var cvss4Values = map[string][]string{
	"AV": {"N", "A", "L", "P"}, "AC": {"L", "H"}, "AT": {"N", "P"}, "PR": {"N", "L", "H"}, "UI": {"N", "P", "A"},
	"VC": {"H", "L", "N"}, "VI": {"H", "L", "N"}, "VA": {"H", "L", "N"}, "SC": {"H", "L", "N"}, "SI": {"H", "L", "N"}, "SA": {"H", "L", "N"},
	"E":  {"X", "A", "P", "U"},
	"CR": {"X", "H", "M", "L"}, "IR": {"X", "H", "M", "L"}, "AR": {"X", "H", "M", "L"},
	"MAV": {"X", "N", "A", "L", "P"}, "MAC": {"X", "L", "H"}, "MAT": {"X", "N", "P"}, "MPR": {"X", "N", "L", "H"}, "MUI": {"X", "N", "P", "A"},
	"MVC": {"X", "H", "L", "N"}, "MVI": {"X", "H", "L", "N"}, "MVA": {"X", "H", "L", "N"},
	"MSC": {"X", "H", "L", "N"}, "MSI": {"X", "S", "H", "L", "N"}, "MSA": {"X", "S", "H", "L", "N"},
	"S": {"X", "N", "P"}, "AU": {"X", "N", "Y"}, "R": {"X", "A", "U", "I"}, "V": {"X", "D", "C"},
	"RE": {"X", "L", "M", "H"}, "U": {"X", "Clear", "Green", "Amber", "Red"},
}

type cvss4 map[string]string

// m renvoie la valeur effective d'une métrique (modifiée si renseignée, valeurs par défaut des exigences).
func (v cvss4) m(k string) string {
	sel := v[k]
	switch {
	case k == "E" && sel == "X":
		return "A"
	case (k == "CR" || k == "IR" || k == "AR") && sel == "X":
		return "H"
	}
	if mod, ok := v["M"+k]; ok && mod != "X" {
		return mod
	}
	return sel
}

// cvss4Score calcule le score d'un vecteur CVSS v4.0 (base, et menace ou environnement s'ils sont fournis).
func cvss4Score(vec string) (float64, error) {
	raw, err := parseVector(vec, "CVSS:4.0/")
	if err != nil {
		return 0, err
	}
	for k, val := range raw {
		vals, ok := cvss4Values[k]
		if !ok {
			return 0, errVector
		}
		found := false
		for _, x := range vals {
			found = found || x == val
		}
		if !found {
			return 0, errVector
		}
	}
	for _, k := range []string{"AV", "AC", "AT", "PR", "UI", "VC", "VI", "VA", "SC", "SI", "SA"} {
		if _, ok := raw[k]; !ok {
			return 0, errVector
		}
	}
	v := cvss4{}
	for k, val := range raw {
		v[k] = val
	}
	for _, k := range []string{"MAV", "MAC", "MAT", "MPR", "MUI", "MVC", "MVI", "MVA", "MSC", "MSI", "MSA"} {
		if x, ok := v[k]; !ok || x == "X" {
			v[k] = v[k[1:]]
		}
	}
	for _, k := range []string{"S", "AU", "R", "V", "RE", "U", "CR", "IR", "AR", "E"} {
		if _, ok := v[k]; !ok {
			v[k] = "X"
		}
	}
	return v.score(), nil
}

func (v cvss4) macroVector() [6]int {
	var eq [6]int
	av, pr, ui := v.m("AV"), v.m("PR"), v.m("UI")
	switch {
	case av == "N" && pr == "N" && ui == "N":
		eq[0] = 0
	case (av == "N" || pr == "N" || ui == "N") && av != "P":
		eq[0] = 1
	default:
		eq[0] = 2
	}
	if v.m("AC") != "L" || v.m("AT") != "N" {
		eq[1] = 1
	}
	vc, vi, va := v.m("VC"), v.m("VI"), v.m("VA")
	switch {
	case vc == "H" && vi == "H":
		eq[2] = 0
	case vc == "H" || vi == "H" || va == "H":
		eq[2] = 1
	default:
		eq[2] = 2
	}
	switch {
	case v.m("MSI") == "S" || v.m("MSA") == "S":
		eq[3] = 0
	case v.m("SC") == "H" || v.m("SI") == "H" || v.m("SA") == "H":
		eq[3] = 1
	default:
		eq[3] = 2
	}
	switch v.m("E") {
	case "P":
		eq[4] = 1
	case "U":
		eq[4] = 2
	}
	if !(v.m("CR") == "H" && vc == "H" || v.m("IR") == "H" && vi == "H" || v.m("AR") == "H" && va == "H") {
		eq[5] = 1
	}
	return eq
}

func macroKey(eq [6]int) string {
	b := make([]byte, 6)
	for i, x := range eq {
		b[i] = byte('0' + x)
	}
	return string(b)
}

func lookup4(eq [6]int) float64 {
	if s, ok := cvss4Lookup[macroKey(eq)]; ok {
		return s
	}
	return math.NaN()
}

var cvss4Levels = map[string]map[string]float64{
	"AV": {"N": 0, "A": 0.1, "L": 0.2, "P": 0.3},
	"PR": {"N": 0, "L": 0.1, "H": 0.2},
	"UI": {"N": 0, "P": 0.1, "A": 0.2},
	"AC": {"L": 0, "H": 0.1},
	"AT": {"N": 0, "P": 0.1},
	"VC": {"H": 0, "L": 0.1, "N": 0.2},
	"VI": {"H": 0, "L": 0.1, "N": 0.2},
	"VA": {"H": 0, "L": 0.1, "N": 0.2},
	"SC": {"H": 0.1, "L": 0.2, "N": 0.3},
	"SI": {"S": 0, "H": 0.1, "L": 0.2, "N": 0.3},
	"SA": {"S": 0, "H": 0.1, "L": 0.2, "N": 0.3},
	"CR": {"H": 0, "M": 0.1, "L": 0.2},
	"IR": {"H": 0, "M": 0.1, "L": 0.2},
	"AR": {"H": 0, "M": 0.1, "L": 0.2},
}

// Vecteurs les plus sévères de chaque classe d'équivalence.
var (
	cvss4MaxEQ1 = [][]string{{"AV:N/PR:N/UI:N"}, {"AV:A/PR:N/UI:N", "AV:N/PR:L/UI:N", "AV:N/PR:N/UI:P"}, {"AV:P/PR:N/UI:N", "AV:A/PR:L/UI:P"}}
	cvss4MaxEQ2 = [][]string{{"AC:L/AT:N"}, {"AC:H/AT:N", "AC:L/AT:P"}}
	// [eq3][eq6]
	cvss4MaxEQ36 = [3][2][]string{
		{{"VC:H/VI:H/VA:H/CR:H/IR:H/AR:H"}, {"VC:H/VI:H/VA:L/CR:M/IR:M/AR:H", "VC:H/VI:H/VA:H/CR:M/IR:M/AR:M"}},
		{{"VC:L/VI:H/VA:H/CR:H/IR:H/AR:H", "VC:H/VI:L/VA:H/CR:H/IR:H/AR:H"}, {"VC:L/VI:H/VA:L/CR:H/IR:M/AR:H", "VC:L/VI:H/VA:H/CR:H/IR:M/AR:M", "VC:H/VI:L/VA:H/CR:M/IR:H/AR:M", "VC:H/VI:L/VA:L/CR:M/IR:H/AR:H", "VC:L/VI:L/VA:H/CR:H/IR:H/AR:M"}},
		{nil, {"VC:L/VI:L/VA:L/CR:H/IR:H/AR:H"}},
	}
	cvss4MaxEQ4 = [][]string{{"SC:H/SI:S/SA:S"}, {"SC:H/SI:H/SA:H"}, {"SC:L/SI:L/SA:L"}}
	cvss4MaxEQ5 = [][]string{{"E:A"}, {"E:P"}, {"E:U"}}

	cvss4MaxSeverityEQ1  = []float64{1, 4, 5}
	cvss4MaxSeverityEQ2  = []float64{1, 2}
	cvss4MaxSeverityEQ36 = [3][2]float64{{7, 6}, {8, 8}, {0, 10}}
	cvss4MaxSeverityEQ4  = []float64{6, 5, 4}
)

func (v cvss4) score() float64 {
	all := true
	for _, k := range []string{"VC", "VI", "VA", "SC", "SI", "SA"} {
		all = all && v.m(k) == "N"
	}
	if all {
		return 0
	}
	eq := v.macroVector()
	value := lookup4(eq)
	next := func(i int) float64 {
		n := eq
		n[i]++
		return lookup4(n)
	}
	lowerEQ1, lowerEQ2, lowerEQ4, lowerEQ5 := next(0), next(1), next(3), next(4)
	var lowerEQ36 float64
	switch e3, e6 := eq[2], eq[5]; {
	case e3 == 1 && e6 == 1, e3 == 0 && e6 == 1:
		lowerEQ36 = next(2)
	case e3 == 1 && e6 == 0:
		lowerEQ36 = next(5)
	case e3 == 0 && e6 == 0:
		left, right := next(5), next(2)
		// max() de Python : NaN à gauche l'emporte, comme dans le calculateur de référence.
		lowerEQ36 = left
		if !math.IsNaN(left) && right > left {
			lowerEQ36 = right
		}
	default:
		n := eq
		n[2]++
		n[5]++
		lowerEQ36 = lookup4(n)
	}

	var maxVectors []string
	for _, a := range cvss4MaxEQ1[eq[0]] {
		for _, b := range cvss4MaxEQ2[eq[1]] {
			for _, c := range cvss4MaxEQ36[eq[2]][eq[5]] {
				for _, d := range cvss4MaxEQ4[eq[3]] {
					for _, e := range cvss4MaxEQ5[eq[4]] {
						maxVectors = append(maxVectors, a+"/"+b+"/"+c+"/"+d+"/"+e)
					}
				}
			}
		}
	}
	dist := map[string]float64{}
	for _, mv := range maxVectors {
		ref := map[string]string{}
		for _, f := range strings.Split(mv, "/") {
			k, val, _ := strings.Cut(f, ":")
			ref[k] = val
		}
		negative := false
		for k, levels := range cvss4Levels {
			dist[k] = levels[v.m(k)] - levels[ref[k]]
			negative = negative || dist[k] < 0
		}
		if !negative {
			break
		}
	}
	curEQ1 := dist["AV"] + dist["PR"] + dist["UI"]
	curEQ2 := dist["AC"] + dist["AT"]
	curEQ36 := dist["VC"] + dist["VI"] + dist["VA"] + dist["CR"] + dist["IR"] + dist["AR"]
	curEQ4 := dist["SC"] + dist["SI"] + dist["SA"]

	const step = 0.1
	n := 0
	var sum float64
	add := func(available, current, maxSeverity float64) {
		if math.IsNaN(available) || available < 0 {
			return
		}
		n++
		if maxSeverity > 0 {
			sum += available * (current / (maxSeverity * step))
		}
	}
	add(value-lowerEQ1, curEQ1, cvss4MaxSeverityEQ1[eq[0]])
	add(value-lowerEQ2, curEQ2, cvss4MaxSeverityEQ2[eq[1]])
	add(value-lowerEQ36, curEQ36, cvss4MaxSeverityEQ36[eq[2]][eq[5]])
	add(value-lowerEQ4, curEQ4, cvss4MaxSeverityEQ4[eq[3]])
	add(value-lowerEQ5, 0, 1) // EQ5 : pas de distance intra-classe
	if n > 0 {
		value -= sum / float64(n)
	}
	value = math.Max(0, math.Min(10, value))
	return math.Floor((value+1e-6)*10+0.5) / 10
}

// cvss4Lookup : score de chaque macro-vecteur EQ1…EQ6 (spécification CVSS v4.0).
var cvss4Lookup = map[string]float64{
	"000000": 10, "000001": 9.9, "000010": 9.8, "000011": 9.5, "000020": 9.5, "000021": 9.2, "000100": 10, "000101": 9.6,
	"000110": 9.3, "000111": 8.7, "000120": 9.1, "000121": 8.1, "000200": 9.3, "000201": 9, "000210": 8.9, "000211": 8,
	"000220": 8.1, "000221": 6.8, "001000": 9.8, "001001": 9.5, "001010": 9.5, "001011": 9.2, "001020": 9, "001021": 8.4,
	"001100": 9.3, "001101": 9.2, "001110": 8.9, "001111": 8.1, "001120": 8.1, "001121": 6.5, "001200": 8.8, "001201": 8,
	"001210": 7.8, "001211": 7, "001220": 6.9, "001221": 4.8, "002001": 9.2, "002011": 8.2, "002021": 7.2, "002101": 7.9,
	"002111": 6.9, "002121": 5, "002201": 6.9, "002211": 5.5, "002221": 2.7, "010000": 9.9, "010001": 9.7, "010010": 9.5,
	"010011": 9.2, "010020": 9.2, "010021": 8.5, "010100": 9.5, "010101": 9.1, "010110": 9, "010111": 8.3, "010120": 8.4,
	"010121": 7.1, "010200": 9.2, "010201": 8.1, "010210": 8.2, "010211": 7.1, "010220": 7.2, "010221": 5.3, "011000": 9.5,
	"011001": 9.3, "011010": 9.2, "011011": 8.5, "011020": 8.5, "011021": 7.3, "011100": 9.2, "011101": 8.2, "011110": 8,
	"011111": 7.2, "011120": 7, "011121": 5.9, "011200": 8.4, "011201": 7, "011210": 7.1, "011211": 5.2, "011220": 5,
	"011221": 3, "012001": 8.6, "012011": 7.5, "012021": 5.2, "012101": 7.1, "012111": 5.2, "012121": 2.9, "012201": 6.3,
	"012211": 2.9, "012221": 1.7, "100000": 9.8, "100001": 9.5, "100010": 9.4, "100011": 8.7, "100020": 9.1, "100021": 8.1,
	"100100": 9.4, "100101": 8.9, "100110": 8.6, "100111": 7.4, "100120": 7.7, "100121": 6.4, "100200": 8.7, "100201": 7.5,
	"100210": 7.4, "100211": 6.3, "100220": 6.3, "100221": 4.9, "101000": 9.4, "101001": 8.9, "101010": 8.8, "101011": 7.7,
	"101020": 7.6, "101021": 6.7, "101100": 8.6, "101101": 7.6, "101110": 7.4, "101111": 5.8, "101120": 5.9, "101121": 5,
	"101200": 7.2, "101201": 5.7, "101210": 5.7, "101211": 5.2, "101220": 5.2, "101221": 2.5, "102001": 8.3, "102011": 7,
	"102021": 5.4, "102101": 6.5, "102111": 5.8, "102121": 2.6, "102201": 5.3, "102211": 2.1, "102221": 1.3, "110000": 9.5,
	"110001": 9, "110010": 8.8, "110011": 7.6, "110020": 7.6, "110021": 7, "110100": 9, "110101": 7.7, "110110": 7.5,
	"110111": 6.2, "110120": 6.1, "110121": 5.3, "110200": 7.7, "110201": 6.6, "110210": 6.8, "110211": 5.9, "110220": 5.2,
	"110221": 3, "111000": 8.9, "111001": 7.8, "111010": 7.6, "111011": 6.7, "111020": 6.2, "111021": 5.8, "111100": 7.4,
	"111101": 5.9, "111110": 5.7, "111111": 5.7, "111120": 4.7, "111121": 2.3, "111200": 6.1, "111201": 5.2, "111210": 5.7,
	"111211": 2.9, "111220": 2.4, "111221": 1.6, "112001": 7.1, "112011": 5.9, "112021": 3, "112101": 5.8, "112111": 2.6,
	"112121": 1.5, "112201": 2.3, "112211": 1.3, "112221": 0.6, "200000": 9.3, "200001": 8.7, "200010": 8.6, "200011": 7.2,
	"200020": 7.5, "200021": 5.8, "200100": 8.6, "200101": 7.4, "200110": 7.4, "200111": 6.1, "200120": 5.6, "200121": 3.4,
	"200200": 7, "200201": 5.4, "200210": 5.2, "200211": 4, "200220": 4, "200221": 2.2, "201000": 8.5, "201001": 7.5,
	"201010": 7.4, "201011": 5.5, "201020": 6.2, "201021": 5.1, "201100": 7.2, "201101": 5.7, "201110": 5.5, "201111": 4.1,
	"201120": 4.6, "201121": 1.9, "201200": 5.3, "201201": 3.6, "201210": 3.4, "201211": 1.9, "201220": 1.9, "201221": 0.8,
	"202001": 6.4, "202011": 5.1, "202021": 2, "202101": 4.7, "202111": 2.1, "202121": 1.1, "202201": 2.4, "202211": 0.9,
	"202221": 0.4, "210000": 8.8, "210001": 7.5, "210010": 7.3, "210011": 5.3, "210020": 6, "210021": 5, "210100": 7.3,
	"210101": 5.5, "210110": 5.9, "210111": 4, "210120": 4.1, "210121": 2, "210200": 5.4, "210201": 4.3, "210210": 4.5,
	"210211": 2.2, "210220": 2, "210221": 1.1, "211000": 7.5, "211001": 5.5, "211010": 5.8, "211011": 4.5, "211020": 4,
	"211021": 2.1, "211100": 6.1, "211101": 5.1, "211110": 4.8, "211111": 1.8, "211120": 2, "211121": 0.9, "211200": 4.6,
	"211201": 1.8, "211210": 1.7, "211211": 0.7, "211220": 0.8, "211221": 0.2, "212001": 5.3, "212011": 2.4, "212021": 1.4,
	"212101": 2.4, "212111": 1.2, "212121": 0.5, "212201": 1, "212211": 0.3, "212221": 0.1,
}

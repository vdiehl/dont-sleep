package powercfg

import "testing"

// These samples mirror real `powercfg /query SCHEME_CURRENT <sub> <guid>` output
// in different Windows display languages. The parser must read the SAME AC/DC
// values regardless of language — that's the fix for the "everything shows n/a"
// bug on non-English Windows.
func TestParseCurrentValues(t *testing.T) {
	cases := []struct {
		name   string
		out    string
		wantAC int
		wantDC int
		wantOK bool
	}{
		{
			name: "english",
			out: `
Power Scheme GUID: 381b4222-f694-41f0-9685-ff5bb260df2e  (Balanced)
  Subgroup GUID: 238c9fa8-0aad-41ed-83f4-97be242c8f20  (Sleep)
    Power Setting GUID: 29f6c1db-86da-48c5-9fdb-f2b67b1f44da  (Sleep after)
      Minimum Possible Setting: 0x00000000
      Maximum Possible Setting: 0xffffffff
      Possible Settings increment: 0x00000001
      Possible Settings units: Seconds
    Current AC Power Setting Index: 0x00000384
    Current DC Power Setting Index: 0x00000258`,
			wantAC: 900, wantDC: 600, wantOK: true,
		},
		{
			// Portuguese (Brazil): labels say "CA"/"CC", not "AC"/"DC".
			// The old substring parser produced n/a here; the positional parser must not.
			name: "portuguese",
			out: `
GUID do Esquema de Energia: 381b4222-f694-41f0-9685-ff5bb260df2e  (Equilibrado)
  GUID do Subgrupo: 238c9fa8-0aad-41ed-83f4-97be242c8f20  (Suspender)
    GUID da Configuracao de Energia: 29f6c1db-86da-48c5-9fdb-f2b67b1f44da  (Suspender apos)
      Configuracao Minima Possivel: 0x00000000
      Configuracao Maxima Possivel: 0xffffffff
      Incremento de Configuracoes Possiveis: 0x00000001
      Unidades de Configuracoes Possiveis: Segundos
    Indice de Configuracao de Energia de CA Atual: 0x00000384
    Indice de Configuracao de Energia de CC Atual: 0x00000258`,
			wantAC: 900, wantDC: 600, wantOK: true,
		},
		{
			// Enumerated setting (e.g. idle disable): possible values listed, then AC/DC.
			name: "enumerated",
			out: `
    Power Setting GUID: 5d76a2ca-e8c0-402f-a133-2158492d58ad  (Processor idle disable)
      Possible Setting Index: 0x00000000
      Possible Setting Friendly Name: Disable Idle
      Possible Setting Index: 0x00000001
      Possible Setting Friendly Name: Enable Idle
    Current AC Power Setting Index: 0x00000001
    Current DC Power Setting Index: 0x00000000`,
			wantAC: 1, wantDC: 0, wantOK: true,
		},
		{
			name:   "empty (hidden/absent setting)",
			out:    "  GUID Alias: SCHEME_BALANCED",
			wantOK: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ac, dc, ok := parseCurrentValues(c.out)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if ac != c.wantAC || dc != c.wantDC {
				t.Fatalf("got AC=%d DC=%d, want AC=%d DC=%d", ac, dc, c.wantAC, c.wantDC)
			}
		})
	}
}

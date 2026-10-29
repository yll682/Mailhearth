package mailops

import "testing"

// #21: control and bidirectional override characters never reach a
// download name.
func TestSafeFilenameStripsControlAndBidi(t *testing.T) {
	cases := map[string]string{
		"invoice\u202efdp.exe": "invoice_fdp.exe",
		"a\u2066b\u2069.txt":   "a_b_.txt",
		"x\u0085y\x7fz.txt":    "x_y_z.txt",
		"../../etc/passwd":     "passwd",
		"报告.pdf":               "报告.pdf",
	}
	for in, want := range cases {
		if got := SafeFilename(in, "fallback"); got != want {
			t.Errorf("SafeFilename(%q) = %q，要求 %q", in, got, want)
		}
	}
}

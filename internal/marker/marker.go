// Package marker provides a general-purpose mechanism for signaling
// directory changes to the shell hook wrapper.
package marker

import (
	"fmt"
	"io"

	"github.com/Broderick-Westrope/wtp/v3/internal/procenv"
)

// Prefix is the marker prefix that the shell hook scans for in stderr.
const Prefix = "__wtp_cd:"

// Emit writes the cd marker to the given writer.
// It only emits when __WTP_HOOKED is set to "1" in environ ("KEY=value" entries),
// preventing confusing output when wtp is called directly (without the shell hook).
func Emit(w io.Writer, environ []string, path string) error {
	if procenv.Lookup(environ, "__WTP_HOOKED") != "1" {
		return nil
	}
	_, err := fmt.Fprintf(w, "%s%s\n", Prefix, path)
	return err
}

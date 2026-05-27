// Package marker provides a general-purpose mechanism for signaling
// directory changes to the shell hook wrapper.
package marker

import (
	"fmt"
	"io"
	"os"
)

// Prefix is the marker prefix that the shell hook scans for in stderr.
const Prefix = "__wtp_cd:"

// Emit writes the cd marker to the given writer.
// It only emits when the __WTP_HOOKED env var is set to "1",
// preventing confusing output when wtp is called directly (without the shell hook).
func Emit(w io.Writer, path string) error {
	if os.Getenv("__WTP_HOOKED") != "1" {
		return nil
	}
	_, err := fmt.Fprintf(w, "%s%s\n", Prefix, path)
	return err
}

package machine

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// tcpListen is the LISTEN state in /proc/net/tcp's st column.
const tcpListen = "0A"

// Listening returns the TCP ports in the LISTEN state in <procRoot>/net/tcp
// and <procRoot>/net/tcp6, on any local address. A missing tcp6 (IPv6
// disabled) lists none; a missing tcp is an error.
func Listening(procRoot string) (map[int]bool, error) {
	ports := map[int]bool{}
	for _, name := range []string{"tcp", "tcp6"} {
		path := filepath.Join(procRoot, "net", name)
		data, err := os.ReadFile(path)
		if name == "tcp6" && errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Scan() // the header
		for sc.Scan() {
			// sl local_address rem_address st ...
			f := strings.Fields(sc.Text())
			if len(f) < 4 || f[3] != tcpListen {
				continue
			}
			i := strings.LastIndexByte(f[1], ':')
			port, err := strconv.ParseUint(f[1][i+1:], 16, 16)
			if i < 0 || err != nil {
				return nil, fmt.Errorf("%s: local address %q has no hex port", path, f[1])
			}
			ports[int(port)] = true
		}
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	return ports, nil
}

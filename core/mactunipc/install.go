package mactunipc

import (
	"fmt"
	"path"
	"strconv"
	"strings"
)

const helperDirectory = "/Library/PrivilegedHelperTools"

// All executable names, destinations and command syntax are fixed. The only
// variable strings are one absolute bundled source path and a checked digest;
// every shell argument is single-quoted, even numeric process identity fields.
func shellQuote(s string) (string, error) {
	if strings.ContainsRune(s, 0) {
		return "", ErrHelperInvalid
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'", nil
}

func InstallationCommand(source, hash string, owner Identity) (string, error) {
	if !path.IsAbs(source) || strings.ContainsAny(source, "\r\n") || !validHash(hash) || !owner.valid() {
		return "", ErrHelperInvalid
	}
	args := []string{installScript, "bluegrass-install", source, hash, strconv.FormatUint(uint64(owner.UID), 10), strconv.FormatUint(uint64(owner.PID), 10), strconv.FormatInt(owner.StartSeconds, 10), strconv.FormatInt(owner.StartMicroseconds, 10)}
	var out strings.Builder
	out.WriteString("/bin/sh -c")
	for _, a := range args {
		q, err := shellQuote(a)
		if err != nil {
			return "", err
		}
		out.WriteByte(' ')
		out.WriteString(q)
	}
	return out.String(), nil
}

// osascript runs a fixed program, with the generated command passed as a single
// argv value. AppleScript's parser never sees a path, hash or profile value.
// No output/error details from a shell or the password dialog reach callers.
const authorizationScript = `on run argv
try
    with timeout of 70 seconds
        do shell script (item 1 of argv) with administrator privileges
    end timeout
    return "OK"
on error errText number errNumber
    if errNumber is -128 then return "AUTHORIZATION_CANCELLED"
    if errNumber is -1712 then return "AUTHORIZATION_TIMEOUT"
    if errNumber is 80 then return "PRIVILEGED_HELPER_INVALID"
    return "SERVICE_UNAVAILABLE"
end try
end run`

// The root shell only copies and hashes data until the root-owned post-copy
// hash matches the broker's compiled digest. No source bytes are executed.
// The temporary file and final executable live in a root-only-writable parent.
// Parents and existing destinations are checked without following symlinks.
const installValidation = `
fail() { exit 80; }
noacl() {
    ( /bin/ls -ldne "$1" && /usr/bin/printf '%s\n' BLUEGRASS_NO_ACL_CHECK_OK ) |
        /usr/bin/awk '` + noACLListingFilter + `' || fail
}
rootdir() {
    [ ! -L "$1" ] && [ -d "$1" ] || fail
    [ "$(/usr/bin/stat -f %u "$1")" = 0 ] || fail
    mode=$(/usr/bin/stat -f %Lp "$1")
    [ "$((0$mode & 022))" -eq 0 ] || fail
    noacl "$1"
}
newdir() {
    if [ ! -e "$1" ] && [ ! -L "$1" ]; then /bin/mkdir -m "$2" "$1" || fail; fi
    rootdir "$1"
}
rootfile() {
    [ ! -L "$1" ] && [ -f "$1" ] || fail
    [ "$(/usr/bin/stat -f %u "$1")" = 0 ] || fail
    [ "$(/usr/bin/stat -f %l "$1")" = 1 ] || fail
    mode=$(/usr/bin/stat -f %Lp "$1")
    [ "$((0$mode & 022))" -eq 0 ] || fail
    noacl "$1"
}
`

const installScript = `set -eu
PATH=/usr/bin:/bin:/usr/sbin:/sbin
LC_ALL=C
LANG=C
export PATH LC_ALL LANG
umask 077
` + installValidation + `
digest() { /usr/bin/shasum -a 256 < "$1" | /usr/bin/cut -c 1-64; }
src=$1
sha=$2
uid=$3
pid=$4
sec=$5
usec=$6
[ "$(/usr/bin/id -u)" = 0 ] || fail
/bin/kill -0 "$pid" 2>/dev/null || exit 1
rootdir /
rootdir /Library
newdir /Library/PrivilegedHelperTools 755
rootdir '/Library/Application Support'
newdir '/Library/Application Support/BlueGrass' 755
newdir '/Library/Application Support/BlueGrass/Run' 755
newdir '/Library/Application Support/BlueGrass/Tunnel' 700
dst=/Library/PrivilegedHelperTools/BlueGrass.Tunnel-$sha
[ ! -L "$src" ] && [ -f "$src" ] || fail
noacl "$src"
[ "$(digest "$src")" = "$sha" ] || fail
if [ -e "$dst" ] || [ -L "$dst" ]; then
    rootfile "$dst"
    [ "$(digest "$dst")" = "$sha" ] || fail
else
    tmp=$(/usr/bin/mktemp /Library/PrivilegedHelperTools/.BlueGrass.Tunnel.XXXXXXXX) || fail
    trap '/bin/rm -f "$tmp"' EXIT HUP INT TERM
    rootfile "$tmp"
    /bin/cat < "$src" > "$tmp" || fail
    [ "$(digest "$tmp")" = "$sha" ] || fail
    /usr/sbin/chown 0:0 "$tmp" || fail
    /bin/chmod 700 "$tmp" || fail
    rootfile "$tmp"
    /bin/mv -n "$tmp" "$dst" || fail
    /bin/rm -f "$tmp"
    trap - EXIT HUP INT TERM
fi
rootfile "$dst"
[ "$(digest "$dst")" = "$sha" ] || fail
rootdir /Library/PrivilegedHelperTools
rootfile "$dst"
"$dst" --owner-uid "$uid" --owner-pid "$pid" --owner-start-sec "$sec" --owner-start-usec "$usec" </dev/null >/dev/null 2>&1 &
`

// ParseOwner accepts only the fixed numeric worker arguments, with no config,
// command, socket path or profile path options.
func ParseOwner(args []string) (Identity, error) {
	var owner Identity
	if len(args) != 8 {
		return owner, ErrHelperInvalid
	}
	keys := []string{"--owner-uid", "--owner-pid", "--owner-start-sec", "--owner-start-usec"}
	values := make([]uint64, 4)
	for i, k := range keys {
		if args[i*2] != k {
			return Identity{}, ErrHelperInvalid
		}
		s := args[i*2+1]
		for _, c := range s {
			if c < '0' || c > '9' {
				return Identity{}, ErrHelperInvalid
			}
		}
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return Identity{}, ErrHelperInvalid
		}
		values[i] = n
	}
	if values[0] > 0xffffffff || values[1] > 0x7fffffff || values[2] > 0x7fffffffffffffff || values[3] >= 1000000 {
		return Identity{}, ErrHelperInvalid
	}
	owner = Identity{uint32(values[0]), uint32(values[1]), int64(values[2]), int64(values[3])}
	if !owner.valid() {
		return Identity{}, ErrHelperInvalid
	}
	return owner, nil
}

func helperPath(hash string) string {
	return fmt.Sprintf("%s/BlueGrass.Tunnel-%s", helperDirectory, hash)
}

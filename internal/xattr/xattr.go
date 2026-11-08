// Package xattr records where a download came from, using the freedesktop
// user.xdg.origin.url / user.xdg.referrer.url attributes.
package xattr

import "net/url"

// StampOrigin sets the origin (and, if non-empty, referrer) attributes on path.
// Userinfo is stripped from both URLs so credentials never land on disk.
// Filesystems without xattr support are ignored.
func StampOrigin(path, origin, referrer string) error {
	o, err := stripUserinfo(origin)
	if err != nil {
		return err
	}
	if err := set(path, "xdg.origin.url", o); err != nil {
		return err
	}
	if referrer == "" {
		return nil
	}
	r, err := stripUserinfo(referrer)
	if err != nil {
		return err
	}
	return set(path, "xdg.referrer.url", r)
}

func stripUserinfo(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil {
		return "", err
	}
	u.User = nil
	return u.String(), nil
}

package permissions

import (
	"fmt"
	"os/user"
	"strconv"
)

func GetUserIdUints(u *user.User) (uid, gid uint32, err error) {
	newUID, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("error parsing uid '%s': %w", u.Uid, err)
	}

	newGID, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("error parsing gid '%s': %w", u.Gid, err)
	}

	return uint32(newUID), uint32(newGID), nil
}

func GetUserIdInts(u *user.User) (uid, gid int, err error) {
	newUID, err := strconv.ParseInt(u.Uid, 10, strconv.IntSize)
	if err != nil {
		return 0, 0, fmt.Errorf("error parsing uid '%s': %w", u.Uid, err)
	}

	newGID, err := strconv.ParseInt(u.Gid, 10, strconv.IntSize)
	if err != nil {
		return 0, 0, fmt.Errorf("error parsing gid '%s': %w", u.Gid, err)
	}

	return int(newUID), int(newGID), nil
}

func GetUser(username string) (u *user.User, err error) {
	u, err = user.Lookup(username)
	if err != nil {
		return nil, fmt.Errorf("error looking up user '%s': %w", username, err)
	}

	return u, nil
}

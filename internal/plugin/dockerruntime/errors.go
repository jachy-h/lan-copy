package dockerruntime

import (
	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"

	"lan-copy/internal/plugin"
)

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if client.IsErrConnectionFailed(err) || errdefs.IsUnavailable(err) {
		return plugin.ErrRuntimeUnavailable
	}
	switch {
	case errdefs.IsNotFound(err):
		return plugin.ErrNotFound
	case errdefs.IsConflict(err):
		return plugin.ErrConflict
	}
	return err
}

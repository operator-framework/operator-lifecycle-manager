package containerdregistry

import (
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/metadata"
)

type Store interface {
	Images() images.Store
	Content() content.Store
}

type store struct {
	cs content.Store
	is images.Store
}

func newStore(db *metadata.DB) *store {
	return &store{
		cs: db.ContentStore(),
		is: metadata.NewImageStore(db),
	}
}

func (s *store) Content() content.Store {
	return s.cs
}

func (s *store) Images() images.Store {
	return s.is
}

package catalogstore

import "spacemolt-lt/internal/store"

type Store struct{ *store.FileStore }

func New(path string) *Store { return &Store{FileStore: store.New(path)} }


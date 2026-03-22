package optimizer

import (
    "context"

    "promptguru/internal/optimizer/gepa"
    "promptguru/internal/store"
)

type DatasetLoader struct {
    store store.Store
}

func NewDatasetLoader(st store.Store) *DatasetLoader {
    return &DatasetLoader{store: st}
}

func (d *DatasetLoader) Load(ctx context.Context, keyHash, groupID string) (gepa.Dataset, error) {
    return d.store.LoadDataset(ctx, keyHash, groupID)
}

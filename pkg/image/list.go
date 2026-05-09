package image

import "sort"

type Info struct {
	Name       string
	Repository string
	Tag        string
	ID         string
	CreatedAt  string
	SizeBytes  int64
}

func List() ([]Info, error) {
	index, err := loadRepositoryIndex()
	if err != nil {
		return nil, err
	}

	images := make([]Info, 0, len(index.Images))
	for _, item := range index.Images {
		var size int64
		for _, layer := range item.Layers {
			size += layer.Size
		}
		images = append(images, Info{
			Name:       item.Name,
			Repository: trimLibraryPrefix(item.Repository),
			Tag:        displayReferenceTag(item.Reference),
			ID:         item.ImageID,
			CreatedAt:  item.PulledAt,
			SizeBytes:  size,
		})
	}

	sort.Slice(images, func(i, j int) bool {
		return images[i].CreatedAt > images[j].CreatedAt
	})
	return images, nil
}

func displayReferenceTag(reference string) string {
	if reference == "" {
		return "latest"
	}
	if len(reference) >= 7 && reference[:7] == "sha256:" {
		return "<none>"
	}
	return reference
}

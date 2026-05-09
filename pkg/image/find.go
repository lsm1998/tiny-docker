package image

import "fmt"

type ImageConfig struct {
	Name       string
	Entrypoint []string
	Cmd        []string
	Env        []string
	WorkingDir string
	Layers     []LayerInfo
}

type LayerInfo struct {
	ChainID string
	CacheID string
}

func FindImage(rawRef string) (ImageConfig, error) {
	ref, err := parseImageReference(rawRef)
	if err != nil {
		return ImageConfig{}, err
	}

	name := normalizedImageName(ref)

	index, err := loadRepositoryIndex()
	if err != nil {
		return ImageConfig{}, err
	}

	img, ok := index.Images[name]
	if !ok {
		return ImageConfig{}, fmt.Errorf("image %q not found locally, pull it first", name)
	}

	layers := make([]LayerInfo, len(img.Layers))
	for i, l := range img.Layers {
		layers[i] = LayerInfo{
			ChainID: l.ChainID,
			CacheID: l.CacheID,
		}
	}

	return ImageConfig{
		Name:       img.Name,
		Entrypoint: img.Entrypoint,
		Cmd:        img.Cmd,
		Env:        img.Env,
		WorkingDir: img.WorkingDir,
		Layers:     layers,
	}, nil
}

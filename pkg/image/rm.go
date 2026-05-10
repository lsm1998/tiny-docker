package image

import (
	"fmt"
	"os"
	"path/filepath"
)

// RemoveImage 删除镜像
func RemoveImage(rawRef string) error {
	ref, err := parseImageReference(rawRef)
	if err != nil {
		return err
	}
	name := normalizedImageName(ref)

	index, err := loadRepositoryIndex()
	if err != nil {
		return err
	}

	img, ok := index.Images[name]
	if !ok {
		return fmt.Errorf("image %q not found", name)
	}

	for _, layer := range img.Layers {
		os.RemoveAll(filepath.Join(OverlayRoot(), layer.CacheID))
		os.RemoveAll(blobPath(layer.Digest))
		os.RemoveAll(filepath.Dir(blobPath(layer.Digest)))
		os.RemoveAll(diffIDMappingPath(layer.Digest))
		os.RemoveAll(layerDBDir(layer.ChainID))
	}

	os.RemoveAll(manifestPath(img.ManifestDigest))
	os.RemoveAll(configPath(img.ConfigDigest))

	delete(index.Images, name)
	return upsertRepositoryIndexDirect(index)
}

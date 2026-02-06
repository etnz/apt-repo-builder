package manifest

import (
	"encoding/json"
	"fmt"
)

// Listener is a callback function that receives events during the build process.
type Listener func(fmt.Stringer)

func jsonString(v interface{}) string {
	b, _ := json.Marshal(map[string]interface{}{
		fmt.Sprintf("%T", v): v,
	})
	return string(b)
}

// EventRepositoryLoadSuccess is emitted when the repository is successfully loaded.
type EventRepositoryLoadSuccess struct {
	Path string `json:"path,omitempty"`
}

func (e EventRepositoryLoadSuccess) String() string { return jsonString(e) }

// EventPackageApplySuccess is emitted when a package is successfully applied.
type EventPackageApplySuccess struct {
	FilePath     string `json:"file_path,omitempty"`
	Package      string `json:"package,omitempty"`
	Version      string `json:"version,omitempty"`
	Architecture string `json:"architecture,omitempty"`
}

func (e EventPackageApplySuccess) String() string { return jsonString(e) }

// EventRepositorySaveSuccess is emitted when the repository is successfully saved.
type EventRepositorySaveSuccess struct {
	Path string `json:"path,omitempty"`
}

func (e EventRepositorySaveSuccess) String() string { return jsonString(e) }

// EventPackageWrite is emitted when a package is written to disk or skipped.
type EventPackageWrite struct {
	Package      string `json:"package,omitempty"`
	Version      string `json:"version,omitempty"`
	Architecture string `json:"architecture,omitempty"`
	Skipped      bool   `json:"skipped,omitempty"`
}

func (e EventPackageWrite) String() string { return jsonString(e) }

// EventFileOperation is emitted when a file is written or skipped during repository generation.
type EventFileOperation struct {
	Path      string `json:"path,omitempty"`
	OldDigest string `json:"old_digest,omitempty"`
	NewDigest string `json:"new_digest,omitempty"`
	Created   bool   `json:"created,omitempty"`
	Updated   bool   `json:"updated,omitempty"`
}

func (e EventFileOperation) String() string { return jsonString(e) }

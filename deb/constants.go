package deb

// ControlField represents a standard field in a Debian control file.
type ControlField string

const (
	FieldPackage       ControlField = "Package"
	FieldVersion       ControlField = "Version"
	FieldArchitecture  ControlField = "Architecture"
	FieldMaintainer    ControlField = "Maintainer"
	FieldDescription   ControlField = "Description"
	FieldSection       ControlField = "Section"
	FieldPriority      ControlField = "Priority"
	FieldHomepage      ControlField = "Homepage"
	FieldEssential     ControlField = "Essential"
	FieldDepends       ControlField = "Depends"
	FieldPreDepends    ControlField = "Pre-Depends"
	FieldRecommends    ControlField = "Recommends"
	FieldSuggests      ControlField = "Suggests"
	FieldEnhances      ControlField = "Enhances"
	FieldConflicts     ControlField = "Conflicts"
	FieldBreaks        ControlField = "Breaks"
	FieldReplaces      ControlField = "Replaces"
	FieldProvides      ControlField = "Provides"
	FieldBuiltUsing    ControlField = "Built-Using"
	FieldSource        ControlField = "Source"
	FieldInstalledSize ControlField = "Installed-Size"
)

// ControlFile represents a standard file found in the control.tar.gz archive.
type ControlFile string

const (
	FileControl   ControlFile = "control"
	FileMd5sums   ControlFile = "md5sums"
	FileConffiles ControlFile = "conffiles"
	FilePreinst   ControlFile = "preinst"
	FilePostinst  ControlFile = "postinst"
	FilePrerm     ControlFile = "prerm"
	FilePostrm    ControlFile = "postrm"
	FileConfig    ControlFile = "config"
	FileTriggers  ControlFile = "triggers"
)

// PackageFile represents a standard file found in the .deb archive (ar format).
type PackageFile string

const (
	PkgDebianBinary PackageFile = "debian-binary"
	PkgControlTarGz PackageFile = "control.tar.gz"
	PkgDataTarGz    PackageFile = "data.tar.gz"
)

// ReleaseField represents a standard field in a Debian Release file.
type ReleaseField string

const (
	RelOrigin               ReleaseField = "Origin"
	RelLabel                ReleaseField = "Label"
	RelSuite                ReleaseField = "Suite"
	RelVersion              ReleaseField = "Version"
	RelCodename             ReleaseField = "Codename"
	RelDate                 ReleaseField = "Date"
	RelValidUntil           ReleaseField = "Valid-Until"
	RelArchitectures        ReleaseField = "Architectures"
	RelComponents           ReleaseField = "Components"
	RelDescription          ReleaseField = "Description"
	RelNotAutomatic         ReleaseField = "NotAutomatic"
	RelButAutomaticUpgrades ReleaseField = "ButAutomaticUpgrades"
	RelAcquireByHash        ReleaseField = "Acquire-By-Hash"
	RelSHA256               ReleaseField = "SHA256"
)

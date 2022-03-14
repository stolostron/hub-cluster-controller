package packagemanifest

type PackageManifest struct {
	DefaultChannel string
	CurrentCSV     string
}

var ocmPackageManifest *PackageManifest

func GetPackageManifest() *PackageManifest {
	return ocmPackageManifest
}

func SetPackageManifest(p *PackageManifest) {
	ocmPackageManifest = p
}

func EnsurePackageManifest(new PackageManifest) bool {
	existing := GetPackageManifest()
	if existing.CurrentCSV != new.CurrentCSV ||
		existing.DefaultChannel != new.DefaultChannel {
		return true
	}
	return false
}

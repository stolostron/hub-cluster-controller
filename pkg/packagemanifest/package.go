package packagemanifest

type PackageManifestInfo struct {
	ACMDefaultChannel string
	ACMCurrentCSV     string
	ACMImages         map[string]string
	MCEDefaultChannel string
	MCECurrentCSV     string
	MCEImages         map[string]string
}

var acmPackageManifestInfo = &PackageManifestInfo{}

func GetPackageManifest() *PackageManifestInfo {
	return acmPackageManifestInfo
}

func SetPackageManifest(p *PackageManifestInfo) {
	acmPackageManifestInfo = p
}

func EnsurePackageManifest(new PackageManifestInfo) bool {
	existing := GetPackageManifest()
	if existing.ACMCurrentCSV != new.ACMCurrentCSV ||
		existing.ACMDefaultChannel != new.ACMDefaultChannel {
		return true
	}
	return false
}

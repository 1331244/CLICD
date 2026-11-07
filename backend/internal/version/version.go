package version

var (
	Version  = "1.1.38"
	Repo     = "1331244/CLICD"
	Revision = "unknown"
)

func Current() string {
	if Version == "" {
		return "dev"
	}
	return Version
}

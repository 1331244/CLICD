package lxc

// Proxmox publishes the template archives on its public HTTP mirror. Pin the
// release, compressed size and SHA-512 from the official template index.
const proxmoxImageURL = "http://download.proxmox.com/images/system/"

type ProxmoxImage struct {
	Filename  string
	SHA512    string
	SizeBytes int64
}

var proxmoxImages = map[string]map[string]ProxmoxImage{
	"amd64": {
		"ubuntu-noble": {
			"ubuntu-24.04-standard_24.04-2_amd64.tar.zst",
			"45c2978e6b97fe292ada95fe06834276015e5739a594db4de2fdfd830fa0c37942e8ae118fc1e32ffd9154b3f9378b592738b668ea3957db41f2907b86f219de",
			141589318,
		},
		"ubuntu-jammy": {
			"ubuntu-22.04-standard_22.04-1_amd64.tar.zst",
			"cf617c11232c3fbce1ba9f7146ac194561411c292c445b2a5c9affe5f09729f5c38315619de4d649ff63e31dd5b536ea081fdde1b7792e6e521d1694ac7c9cb8",
			129824858,
		},
		"debian-trixie": {
			"debian-13-standard_13.6-1_amd64.tar.zst",
			"4c0c27ca6ceab5ef0b84db57825a00f26157ef1854bafe97297813e1cbe8ecb8cc9c453cab6b3b0efe1ba193a50c47ece1e41d950e411b8730b835b71e9e754b",
			129954319,
		},
		"debian-bookworm": {
			"debian-12-standard_12.12-1_amd64.tar.zst",
			"50c85eaaece677a3ebe01cc909b83872e9da2a22c29ae652838afce71e83222fdf40f6accecd7d52b180e912fc1f85ecdf7b3fc4d3027da4d865e509a9e76597",
			123731847,
		},
		"debian-bullseye": {
			"debian-11-standard_11.7-1_amd64.tar.zst",
			"973bb8ee260b5da8c02b023651370d53632ae0b838d878e5cbe38291ca2bcac9ab2d0ef80709e5e27865cfd48a67ff334ea1441d5744e3db372a1fcc84491557",
			122247556,
		},
		"alpine-3.24": {
			"alpine-3.24-default_20261105_amd64.tar.xz",
			"5f2ab800b1cf9b9eed768b0cd4b4c395d5bc00a7eda7a185c89111067a25e7ce28d86ffb6d1b3f3181d43d9935e708c713f6b3937f39a5f2839cb56214889c8b",
			3460064,
		},
		"centos-9-stream": {
			"centos-9-stream-default_20240828_amd64.tar.xz",
			"de6ec0a6ffc2e980d4baa11185765ba925822462f14c387b8e50ffda0f2281e8fb8bebc53c84d39b8073dd543ea98496685c828ab713f2ec0ba56dd25e6ddbef",
			108346464,
		},
		"archlinux-current": {
			"archlinux-base_20260420-1_amd64.tar.zst",
			"a6af26c3760f5683ea17140aae9398e3a793f17e3c1586edcec56fb0ed1ba780722ce4c20eafdb5843899fa229cfe859a381c36b6c754b7e381f8337f2334352",
			145902106,
		},
		"fedora-44": {
			"fedora-44-default_20260713_amd64.tar.xz",
			"868c83c15ba476aed986f324e698ac5d80c59f49440f70036da350d2faf0b117435150fce853ca6d553005da86956f0b71d34b40a94456473ad09fc87395d5c9",
			98797776,
		},
		"rockylinux-10": {
			"rockylinux-10-default_20251001_amd64.tar.xz",
			"025d346a9aa9b27b90c28147cece917659cb320508cf248f59c776a00fd3a7009a9e1e0812217c990398588bb11b5a38abca82c0991a161ba3f27e1c6ee190ac",
			84067256,
		},
	},
	"arm64": {
		"debian-trixie": {
			"debian-13-standard_13.6-1_arm64.tar.zst",
			"9b1e8a5d79cdd8190e5dcd969d2d583ea570e1c2133f21a903a5b0eaad478a47df4a784e5ead59945a9abdebebe3241c1f3781d0a59974e09360f1e256e52838",
			129479452,
		},
		"alpine-3.24": {
			"alpine-3.24-default_20261105_arm64.tar.xz",
			"5cdcbe5feb5a4b2cadf900ec69cb177a30fb91cf3f9bd1a41bbfdb8613a518eb87c3a406f3f8fbe8553b1554adb0588f5522f231e96c9916c1fb2ce3a4ad18e5",
			3643372,
		},
	},
}

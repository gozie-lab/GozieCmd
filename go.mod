module duocommander

go 1.24

require (
	github.com/lxn/walk v0.0.0-20210112085537-c389da54e794
	github.com/lxn/win v0.0.0-20210218163916-a377121e959e
)

require (
	fyne.io/systray v1.12.1 // indirect
	github.com/BurntSushi/toml v1.5.0 // indirect
	github.com/andybalholm/brotli v1.1.0 // indirect
	github.com/bodgit/plumbing v1.3.0 // indirect
	github.com/bodgit/windows v1.0.1 // indirect
	github.com/fredbi/uri v1.1.1 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/fyne-io/gl-js v0.2.0 // indirect
	github.com/fyne-io/glfw-js v0.3.0 // indirect
	github.com/fyne-io/image v0.1.1 // indirect
	github.com/geoffgarside/ber v1.1.0 // indirect
	github.com/go-gl/gl v0.0.0-20231021071112-07e5d0ea2e71 // indirect
	github.com/go-gl/glfw/v3.3/glfw v0.0.0-20240506104042-037f3cc74f2a // indirect
	github.com/go-text/render v0.2.1 // indirect
	github.com/go-text/typesetting v0.3.4 // indirect
	github.com/godbus/dbus/v5 v5.1.0 // indirect
	github.com/hack-pad/go-indexeddb v0.3.2 // indirect
	github.com/hack-pad/safejs v0.1.0 // indirect
	github.com/hashicorp/errwrap v1.0.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/jeandeaual/go-locale v0.0.0-20250612000132-0ef82f21eade // indirect
	github.com/jsummers/gobmp v0.0.0-20230614200233-a9de23ed2e25 // indirect
	github.com/klauspost/compress v1.17.9 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/mattn/go-runewidth v0.0.17 // indirect
	github.com/nfnt/resize v0.0.0-20180221191011-83c6a9932646 // indirect
	github.com/nicksnyder/go-i18n/v2 v2.5.1 // indirect
	github.com/pierrec/lz4/v4 v4.1.21 // indirect
	github.com/rivo/uniseg v0.2.0 // indirect
	github.com/rymdport/portal v0.4.2 // indirect
	github.com/srwiley/oksvg v0.0.0-20221011165216-be6e8873101c // indirect
	github.com/stretchr/testify v1.12.1 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	go4.org v0.0.0-20200411211856-f5505b9728dd // indirect
	gopkg.in/Knetic/govaluate.v3 v3.0.0-00010101000000-000000000000 // indirect
)

replace golang.org/x/sys => github.com/golang/sys v0.30.0

replace gopkg.in/Knetic/govaluate.v3 => github.com/Knetic/govaluate v3.0.0+incompatible

require (
	github.com/bodgit/sevenzip v1.5.2
	github.com/fyne-io/oksvg v0.2.0
	github.com/hirochachacha/go-smb2 v1.1.0
	github.com/jlaffaye/ftp v0.2.4
	github.com/nwaples/rardecode/v2 v2.4.5
	github.com/pkg/sftp v1.13.7
	github.com/srwiley/rasterx v0.0.0-20220730225603-2ab79fcdd4ef
	github.com/studio-b12/gowebdav v0.10.0
	github.com/ulikunitz/xz v0.5.17
	github.com/yeka/zip v0.0.0-20231116150916-03d6312748a9
	github.com/yuin/goldmark v1.8.7
	golang.org/x/crypto v0.33.0
	golang.org/x/image v0.24.0
	golang.org/x/net v0.35.0
	golang.org/x/sys v0.30.0
	golang.org/x/text v0.22.0
)

// go4.org: csak a readerutil és syncutil csomag kell (a 7z-hez), helyi másolatban
replace go4.org => ./stubs/go4

replace golang.org/x/sync => github.com/golang/sync v0.11.0

replace golang.org/x/text => github.com/golang/text v0.22.0

// csak tesztfüggőség; a hálózati szűrő a gopkg.in-t nem engedi
replace gopkg.in/yaml.v3 => ./third_party/yaml3

// Linux-felület (Fyne); a vanity-domainek helyett a GitHub-tükrök
require fyne.io/fyne/v2 v2.7.4

replace (
	fyne.io/fyne/v2 => ./third_party/fyne
	fyne.io/systray => github.com/fyne-io/systray v1.12.1
	golang.org/x/image => github.com/golang/image v0.24.0
	golang.org/x/mod => github.com/golang/mod v0.20.0
	golang.org/x/net => github.com/golang/net v0.35.0
	golang.org/x/tools => github.com/golang/tools v0.24.1
	golang.org/x/tools/go/vcs => ./stubs/vcs
	gopkg.in/check.v1 => ./stubs/check
)

replace go.yaml.in/yaml/v3 => ./third_party/yamlin

replace golang.org/x/crypto => github.com/golang/crypto v0.33.0

replace golang.org/x/term => github.com/golang/term v0.29.0

replace golang.org/x/telemetry => github.com/golang/telemetry v0.0.0-20240521205824-bda55230c457

package update

import "testing"

func TestVersionAndURLValidation(t *testing.T) {
	t.Parallel()

	if got := RepositoryURL(); got != "https://github.com/maybk/veri" {
		t.Fatalf("RepositoryURL() = %q", got)
	}
	if got, err := ReleaseTagURL("v0.2.0"); err != nil || got != "https://github.com/maybk/veri/releases/tag/v0.2.0" {
		t.Fatalf("ReleaseTagURL(v0.2.0) = %q, %v", got, err)
	}
	if _, err := ReleaseTagURL("../evil"); err == nil {
		t.Fatal("expected error for invalid tag")
	}

	if !IsValidReleaseURL("https://github.com/maybk/veri/releases/tag/v0.2.0", "v0.2.0") {
		t.Fatal("expected valid release URL")
	}
	for _, bad := range []string{
		"http://github.com/maybk/veri/releases/tag/v0.2.0",
		"https://evil.example/maybk/veri/releases/tag/v0.2.0",
		"https://github.com/other/veri/releases/tag/v0.2.0",
		"https://github.com/maybk/veri/releases/tag/v0.2.0?x=1",
		"https://github.com/maybk/veri/releases/tag/v0.3.0",
	} {
		if IsValidReleaseURL(bad, "v0.2.0") {
			t.Fatalf("expected invalid release URL: %s", bad)
		}
	}

	asset := "veri-0.2.0-darwin-arm64.tar.gz"
	if !IsValidAssetDownloadURL("https://github.com/maybk/veri/releases/download/v0.2.0/"+asset, "v0.2.0", asset) {
		t.Fatal("expected valid asset download URL")
	}
	for _, bad := range []string{
		"http://github.com/maybk/veri/releases/download/v0.2.0/" + asset,
		"https://github.com/maybk/veri/releases/download/v0.2.0/other.tar.gz",
		"https://github.com/maybk/veri/releases/download/v0.2.0/" + asset + "#frag",
		"https://user:pass@github.com/maybk/veri/releases/download/v0.2.0/" + asset,
	} {
		if IsValidAssetDownloadURL(bad, "v0.2.0", asset) {
			t.Fatalf("expected invalid asset URL: %s", bad)
		}
	}
}

func TestValidateMirrorPrefix(t *testing.T) {
	t.Parallel()

	got, err := ValidateMirrorPrefix("https://mirror.example.com/gh")
	if err != nil || got != "https://mirror.example.com/gh/" {
		t.Fatalf("ValidateMirrorPrefix valid = %q, %v", got, err)
	}
	if empty, err := ValidateMirrorPrefix("   "); err != nil || empty != "" {
		t.Fatalf("ValidateMirrorPrefix empty = %q, %v", empty, err)
	}
	for _, bad := range []string{
		"http://mirror.example.com/",
		"https://user:pass@mirror.example.com/",
		"https://mirror.example.com/?a=1",
		"https://mirror.example.com/#frag",
		"not-a-url",
	} {
		if _, err := ValidateMirrorPrefix(bad); err == nil {
			t.Fatalf("expected error for mirror %q", bad)
		}
	}
}

func TestNormalizeAndCompareVersions(t *testing.T) {
	t.Parallel()

	ver, tag, ok := NormalizeReleaseVersion("v1.2.3")
	if !ok || ver != "1.2.3" || tag != "v1.2.3" {
		t.Fatalf("NormalizeReleaseVersion(v1.2.3) = %q, %q, %v", ver, tag, ok)
	}
	ver, tag, ok = NormalizeReleaseVersion("0.2.0")
	if !ok || ver != "0.2.0" || tag != "v0.2.0" {
		t.Fatalf("NormalizeReleaseVersion(0.2.0) = %q, %q, %v", ver, tag, ok)
	}
	for _, bad := range []string{"", "dev", "0.1", "0.1.0-pre", "v1.2.3/evil"} {
		if _, _, ok := NormalizeReleaseVersion(bad); ok {
			t.Fatalf("expected NormalizeReleaseVersion(%q) to fail", bad)
		}
	}

	cases := []struct {
		cur, lat string
		want     bool
	}{
		{"0.1.0", "0.2.0", true},
		{"0.1.0", "0.1.1", true},
		{"0.1.0-pre", "0.1.0", true},
		{"0.1.0", "0.1.0", false},
		{"0.2.0", "0.1.9", false},
		{"0.1.0", "0.1.0-pre", false},
		{"dev", "0.1.0", false},
	}
	for _, tc := range cases {
		if got := IsNewerVersion(tc.cur, tc.lat); got != tc.want {
			t.Errorf("IsNewerVersion(%q, %q) = %v, want %v", tc.cur, tc.lat, got, tc.want)
		}
	}

	if !IsDevVersion("dev") || !IsDevVersion("0.0.0") || !IsDevVersion("") || IsDevVersion("0.1.0") {
		t.Fatal("IsDevVersion classification failed")
	}
}

func TestExpectedAssetAndChecksums(t *testing.T) {
	t.Parallel()

	name, ok := ExpectedAssetName("0.2.0", "darwin", "arm64")
	if !ok || name != "veri-0.2.0-darwin-arm64.tar.gz" {
		t.Fatalf("darwin/arm64 asset = %q, %v", name, ok)
	}
	name, ok = ExpectedAssetName("0.2.0", "windows", "amd64")
	if !ok || name != "veri-0.2.0-windows-amd64.zip" {
		t.Fatalf("windows/amd64 asset = %q, %v", name, ok)
	}
	if _, ok := ExpectedAssetName("0.2.0", "freebsd", "amd64"); ok {
		t.Fatal("expected freebsd to be unsupported")
	}

	hex64 := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if got, ok := ParseSHA256Digest("sha256:" + hex64); !ok || got != hex64 {
		t.Fatalf("ParseSHA256Digest = %q, %v", got, ok)
	}
	if _, ok := ParseSHA256Digest("sha256:short"); ok {
		t.Fatal("expected short digest to fail")
	}

	sums := "# comment\n" + hex64 + "  *./veri-0.2.0-linux-amd64.tar.gz\n"
	if got, ok := ParseSHA256Sums(sums, "veri-0.2.0-linux-amd64.tar.gz"); !ok || got != hex64 {
		t.Fatalf("ParseSHA256Sums = %q, %v", got, ok)
	}
	if _, ok := ParseSHA256Sums(sums, "veri-0.2.0-linux-arm64.tar.gz"); ok {
		t.Fatal("expected unmatched asset to return false")
	}
}

func TestDetectInstall(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		goos        string
		version     string
		raw         string
		resolved    string
		want        InstallMethod
		wantCmd     string
		wantManaged bool
		wantSelf    bool
	}{
		{
			name:        "mise github backend on macOS",
			goos:        "darwin",
			version:     "0.1.1",
			raw:         "/Users/alice/.local/share/mise/shims/veri",
			resolved:    "/Users/alice/.local/share/mise/installs/github-maybk-veri/0.1.1/veri",
			want:        MethodMise,
			wantCmd:     "mise upgrade github:maybk/veri",
			wantManaged: true,
		},
		{
			name:        "mise npm backend takes precedence over node_modules",
			goos:        "darwin",
			version:     "0.1.1",
			raw:         "/Users/alice/.local/share/mise/installs/npm-maybk-veri/0.1.1/node_modules/@maybk/veri-darwin-arm64/bin/veri",
			resolved:    "/Users/alice/.local/share/mise/installs/npm-maybk-veri/0.1.1/node_modules/@maybk/veri-darwin-arm64/bin/veri",
			want:        MethodMise,
			wantCmd:     "mise upgrade npm:@maybk/veri",
			wantManaged: true,
		},
		{
			name:        "mise ubi backend on linux",
			goos:        "linux",
			version:     "0.1.1",
			raw:         "/home/alice/.local/share/mise/installs/ubi-maybk-veri/0.1.1/veri",
			resolved:    "/home/alice/.local/share/mise/installs/ubi-maybk-veri/0.1.1/veri",
			want:        MethodMise,
			wantCmd:     "mise upgrade ubi:maybk/veri",
			wantManaged: true,
		},
		{
			name:        "mise registry backend on windows",
			goos:        "windows",
			version:     "0.1.1",
			raw:         `C:\Users\alice\AppData\Local\mise\installs\veri\0.1.1\veri.exe`,
			resolved:    `C:\Users\alice\AppData\Local\mise\installs\veri\0.1.1\veri.exe`,
			want:        MethodMise,
			wantCmd:     "mise upgrade veri",
			wantManaged: true,
		},
		{
			name:        "npm global inside mise-managed node falls through to npm",
			goos:        "darwin",
			version:     "0.1.1",
			raw:         "/Users/alice/.local/share/mise/shims/veri",
			resolved:    "/Users/alice/.local/share/mise/installs/node/22.14.0/lib/node_modules/@maybk/veri-darwin-arm64/bin/veri",
			want:        MethodNPM,
			wantCmd:     "npm install -g @maybk/veri@latest",
			wantManaged: true,
		},
		{
			name:        "npm global under homebrew node takes precedence over homebrew",
			goos:        "darwin",
			version:     "0.1.1",
			raw:         "/opt/homebrew/lib/node_modules/@maybk/veri-darwin-arm64/bin/veri",
			resolved:    "/opt/homebrew/lib/node_modules/@maybk/veri-darwin-arm64/bin/veri",
			want:        MethodNPM,
			wantCmd:     "npm install -g @maybk/veri@latest",
			wantManaged: true,
		},
		{
			name:        "npx ephemeral cache detected as npm",
			goos:        "linux",
			version:     "0.1.1",
			raw:         "/home/alice/.npm/_npx/1234abcd/node_modules/@maybk/veri-linux-x64/bin/veri",
			resolved:    "/home/alice/.npm/_npx/1234abcd/node_modules/@maybk/veri-linux-x64/bin/veri",
			want:        MethodNPM,
			wantCmd:     "npm install -g @maybk/veri@latest",
			wantManaged: true,
		},
		{
			name:        "pnpm global on windows",
			goos:        "windows",
			version:     "0.1.1",
			raw:         `C:\Users\alice\AppData\Local\pnpm\global\5\node_modules\.pnpm\@maybk+veri-win32-x64@0.1.1\node_modules\@maybk\veri-win32-x64\bin\veri.exe`,
			resolved:    `C:\Users\alice\AppData\Local\pnpm\global\5\node_modules\.pnpm\@maybk+veri-win32-x64@0.1.1\node_modules\@maybk\veri-win32-x64\bin\veri.exe`,
			want:        MethodNPM,
			wantCmd:     "pnpm add -g @maybk/veri@latest",
			wantManaged: true,
		},
		{
			name:        "bun global on linux",
			goos:        "linux",
			version:     "0.1.1",
			raw:         "/home/alice/.bun/install/global/node_modules/@maybk/veri-linux-arm64/bin/veri",
			resolved:    "/home/alice/.bun/install/global/node_modules/@maybk/veri-linux-arm64/bin/veri",
			want:        MethodNPM,
			wantCmd:     "bun add -g @maybk/veri@latest",
			wantManaged: true,
		},
		{
			name:        "npm global on windows",
			goos:        "windows",
			version:     "0.1.1",
			raw:         `C:\Users\alice\AppData\Roaming\npm\node_modules\@maybk\veri-win32-x64\bin\veri.exe`,
			resolved:    `C:\Users\alice\AppData\Roaming\npm\node_modules\@maybk\veri-win32-x64\bin\veri.exe`,
			want:        MethodNPM,
			wantCmd:     "npm install -g @maybk/veri@latest",
			wantManaged: true,
		},
		{
			name:        "homebrew apple silicon",
			goos:        "darwin",
			version:     "0.1.0",
			raw:         "/opt/homebrew/bin/veri",
			resolved:    "/opt/homebrew/Cellar/veri/0.1.0/bin/veri",
			want:        MethodHomebrew,
			wantCmd:     "brew upgrade veri",
			wantManaged: true,
		},
		{
			name:        "homebrew intel cellar",
			goos:        "darwin",
			version:     "0.1.0",
			raw:         "/usr/local/bin/veri",
			resolved:    "/usr/local/Cellar/veri/0.1.0/bin/veri",
			want:        MethodHomebrew,
			wantCmd:     "brew upgrade veri",
			wantManaged: true,
		},
		{
			name:     "linux system dir",
			goos:     "linux",
			version:  "0.1.0",
			raw:      "/usr/bin/veri",
			resolved: "/usr/bin/veri",
			want:     MethodSystem,
		},
		{
			name:        "windows scoop shim",
			goos:        "windows",
			version:     "0.1.0",
			raw:         `C:\Users\alice\scoop\shims\veri.exe`,
			resolved:    `C:\Users\alice\scoop\apps\veri\0.1.0\veri.exe`,
			want:        MethodScoop,
			wantCmd:     "scoop update veri",
			wantManaged: true,
		},
		{
			name:        "windows winget",
			goos:        "windows",
			version:     "0.1.0",
			raw:         `C:\Users\alice\AppData\Local\Microsoft\WinGet\Packages\maybk.Veri\veri.exe`,
			resolved:    `C:\Users\alice\AppData\Local\Microsoft\WinGet\Packages\maybk.Veri\veri.exe`,
			want:        MethodWinGet,
			wantCmd:     "winget upgrade maybk.Veri",
			wantManaged: true,
		},
		{
			name:     "dev version",
			goos:     "linux",
			version:  "dev",
			raw:      "/home/alice/.local/bin/veri",
			resolved: "/home/alice/.local/bin/veri",
			want:     MethodDev,
		},
		{
			name:     "go run temp binary",
			goos:     "darwin",
			version:  "0.1.0",
			raw:      "/var/folders/xx/go-build123456/b001/exe/veri",
			resolved: "/var/folders/xx/go-build123456/b001/exe/veri",
			want:     MethodDev,
		},
		{
			name:     "standalone user local",
			goos:     "linux",
			version:  "0.1.0",
			raw:      "/home/alice/.local/bin/veri",
			resolved: "/home/alice/.local/bin/veri",
			want:     MethodStandalone,
			wantSelf: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := DetectInstall(tc.goos, tc.version, tc.raw, tc.resolved)
			if got.Method != tc.want {
				t.Fatalf("Method = %q, want %q", got.Method, tc.want)
			}
			if got.ManagedCommand != tc.wantCmd {
				t.Fatalf("ManagedCommand = %q, want %q", got.ManagedCommand, tc.wantCmd)
			}
			if got.IsManagedByPackageManager() != tc.wantManaged {
				t.Fatalf("IsManagedByPackageManager() = %v, want %v", got.IsManagedByPackageManager(), tc.wantManaged)
			}
			if got.CanSelfUpdate() != tc.wantSelf {
				t.Fatalf("CanSelfUpdate() = %v, want %v", got.CanSelfUpdate(), tc.wantSelf)
			}
		})
	}
}

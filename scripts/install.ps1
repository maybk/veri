# veri official installer for Windows (PowerShell 5.1 and PowerShell 7+).
#
# Usage:
#   irm https://raw.githubusercontent.com/maybk/veri/main/scripts/install.ps1 | iex
#
# Environment variables:
#   VERI_VERSION         Specific stable release tag/version (e.g. v0.1.0 or 0.1.0)
#   VERI_INSTALL_DIR     Target directory for veri.exe (default: %LOCALAPPDATA%\veri\bin)
#   VERI_UPDATE_MIRROR   Optional HTTPS mirror prefix for archive download only

param(
    [string]$Version = "",
    [string]$InstallDir = "",
    [string]$Mirror = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# Ensure TLS 1.2 is enabled for Windows PowerShell 5.1.
try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
} catch {
    # PowerShell 7+ / modern .NET manages TLS automatically.
}

$Repo = "maybk/veri"
$Binary = "veri.exe"
$DefaultGitHubBase = "https://github.com/$Repo"
$GitHubBase = if ($env:VERI_GITHUB_BASE) { $env:VERI_GITHUB_BASE.TrimEnd("/") } else { $DefaultGitHubBase }
$IsOfficialBase = ($GitHubBase -eq $DefaultGitHubBase)

if ([string]::IsNullOrWhiteSpace($Version) -and $env:VERI_VERSION) {
    $Version = $env:VERI_VERSION
}
if ([string]::IsNullOrWhiteSpace($InstallDir) -and $env:VERI_INSTALL_DIR) {
    $InstallDir = $env:VERI_INSTALL_DIR
}
if ([string]::IsNullOrWhiteSpace($Mirror) -and $env:VERI_UPDATE_MIRROR) {
    $Mirror = $env:VERI_UPDATE_MIRROR
}

function Fail([string]$Message) {
    Write-Error -Message $Message -ErrorAction Continue
    exit 1
}

function Get-VeriArch {
    try {
        $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
        switch ($arch) {
            "x64"   { return "amd64" }
            "arm64" { return "arm64" }
        }
    } catch {
        # Fallback to environment variables on older PowerShell 5.1 runtimes.
    }

    $raw = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    if ([string]::IsNullOrWhiteSpace($raw)) {
        Fail "Unable to determine Windows CPU architecture"
    }
    switch ($raw.ToUpperInvariant()) {
        "AMD64" { return "amd64" }
        "ARM64" { return "arm64" }
        default { Fail "Unsupported Windows CPU architecture: $raw (supported: amd64, arm64)" }
    }
}

function Validate-Mirror([string]$RawMirror) {
    if ([string]::IsNullOrWhiteSpace($RawMirror)) {
        return ""
    }
    $trimmed = $RawMirror.Trim()
    $uri = $null
    if (-not [Uri]::TryCreate($trimmed, [UriKind]::Absolute, [ref]$uri)) {
        Fail "Invalid mirror prefix: must be a valid https:// URL"
    }
    if ($uri.Scheme -ne "https" -or [string]::IsNullOrWhiteSpace($uri.Host) -or $uri.UserInfo -or $uri.Query -or $uri.Fragment) {
        Fail "Invalid mirror prefix: must be an https:// URL without credentials, query, or fragment"
    }
    return $trimmed.TrimEnd("/") + "/"
}

function Normalize-Version([string]$RawVersion) {
    $trimmed = $RawVersion.Trim()
    if ($trimmed.StartsWith("v") -or $trimmed.StartsWith("V")) {
        $trimmed = $trimmed.Substring(1)
    }
    if ($trimmed -notmatch '^[0-9]+\.[0-9]+\.[0-9]+$') {
        Fail "Invalid release version '$RawVersion' (expected MAJOR.MINOR.PATCH, e.g. v0.1.0)"
    }
    return "v$trimmed"
}

function Resolve-LatestVersion([string]$BaseUrl) {
    $latestUrl = "$BaseUrl/releases/latest"
    $request = [System.Net.HttpWebRequest]::Create($latestUrl)
    $request.Method = "GET"
    $request.AllowAutoRedirect = $true
    $request.MaximumAutomaticRedirections = 10
    $request.UserAgent = "veri-installer"
    $request.Timeout = 15000

    $response = $null
    try {
        $response = $request.GetResponse()
        $finalUri = $response.ResponseUri.AbsoluteUri
    } catch {
        Fail "Failed to resolve latest release from ${latestUrl}: $($_.Exception.Message)"
    } finally {
        if ($null -ne $response) {
            $response.Close()
        }
    }

    if ($finalUri -match '/releases/tag/(v[0-9]+\.[0-9]+\.[0-9]+)(?:[?#].*)?$') {
        return $Matches[1]
    }
    Fail "Could not determine latest stable release from $latestUrl (got: $finalUri); set VERI_VERSION explicitly"
}

function Download-File([string]$Url, [string]$DestPath) {
    if ($IsOfficialBase -and -not $Url.StartsWith("https://")) {
        Fail "Refusing non-HTTPS download URL: $Url"
    }
    $params = @{
        Uri     = $Url
        OutFile = $DestPath
        Headers = @{ "User-Agent" = "veri-installer" }
    }
    if ($PSVersionTable.PSVersion.Major -lt 6) {
        $params["UseBasicParsing"] = $true
    }
    Invoke-WebRequest @params
}

function Parse-ExpectedSha256([string]$SumsPath, [string]$TargetAsset) {
    foreach ($line in Get-Content -Path $SumsPath -Encoding UTF8) {
        $trimmed = $line.Trim()
        if ([string]::IsNullOrWhiteSpace($trimmed) -or $trimmed.StartsWith("#")) {
            continue
        }
        $parts = $trimmed -split '\s+'
        if ($parts.Count -lt 2) {
            continue
        }
        $name = $parts[1].TrimStart('*')
        if ($name.StartsWith("./")) {
            $name = $name.Substring(2)
        }
        if ($name -eq $TargetAsset) {
            $hash = $parts[0].ToLowerInvariant()
            if ($hash -match '^[0-9a-f]{64}$') {
                return $hash
            }
        }
    }
    return ""
}

function Extract-RootBinaryFromZip([string]$ZipPath, [string]$BinaryName, [string]$DestPath) {
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = [System.IO.Compression.ZipFile]::OpenRead($ZipPath)
    try {
        $matched = $null
        foreach ($entry in $archive.Entries) {
            if ([string]::IsNullOrEmpty($entry.Name) -or $entry.FullName.Contains("\")) {
                continue
            }
            $clean = $entry.FullName
            if ($clean.StartsWith("./")) {
                $clean = $clean.Substring(2)
            }
            if ($clean -ceq $BinaryName) {
                if ($null -ne $matched) {
                    Fail "Duplicate root entry '$BinaryName' in archive"
                }
                $matched = $entry
            }
        }
        if ($null -eq $matched) {
            Fail "Root binary '$BinaryName' not found inside archive"
        }
        [System.IO.Compression.ZipFileExtensions]::ExtractToFile($matched, $DestPath, $true)
    } finally {
        $archive.Dispose()
    }
}

function Install-BinarySafely([string]$SourceExe, [string]$TargetDir, [string]$BinaryName) {
    New-Item -ItemType Directory -Force -Path $TargetDir | Out-Null
    $targetPath = Join-Path $TargetDir $BinaryName
    $stagedPath = Join-Path $TargetDir (".$BinaryName.new.$PID")
    $oldPath = "$targetPath.old"

    Copy-Item -Path $SourceExe -Destination $stagedPath -Force
    $backedUp = $false
    try {
        if (Test-Path -LiteralPath $targetPath) {
            if (Test-Path -LiteralPath $oldPath) {
                Remove-Item -LiteralPath $oldPath -Force -ErrorAction SilentlyContinue
            }
            Move-Item -LiteralPath $targetPath -Destination $oldPath -Force
            $backedUp = $true
        }
        Move-Item -LiteralPath $stagedPath -Destination $targetPath -Force
    } catch {
        Remove-Item -LiteralPath $stagedPath -Force -ErrorAction SilentlyContinue
        if ($backedUp -and (Test-Path -LiteralPath $oldPath) -and -not (Test-Path -LiteralPath $targetPath)) {
            Move-Item -LiteralPath $oldPath -Destination $targetPath -Force -ErrorAction SilentlyContinue
        }
        throw
    }

    if ($backedUp) {
        Remove-Item -LiteralPath $oldPath -Force -ErrorAction SilentlyContinue
    }
    return $targetPath
}

function Add-ToUserPath([string]$DirToAdd) {
    $DirToAdd = $DirToAdd.Trim().TrimEnd("\")
    $normTarget = [Environment]::ExpandEnvironmentVariables($DirToAdd).TrimEnd("\").ToLowerInvariant()

    # Preserve REG_EXPAND_SZ entries (such as %USERPROFILE%) in HKCU\Environment.
    $regKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey("Environment", $true)
    if ($null -eq $regKey) {
        $regKey = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey("Environment")
    }
    $addedPersistently = $false
    try {
        $rawPath = [string]$regKey.GetValue("Path", "", [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $segments = @($rawPath -split ";" | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
        $exists = $false
        foreach ($seg in $segments) {
            $expanded = [Environment]::ExpandEnvironmentVariables($seg.Trim()).TrimEnd("\").ToLowerInvariant()
            if ($expanded -eq $normTarget) {
                $exists = $true
                break
            }
        }
        if (-not $exists) {
            $newRawPath = if ([string]::IsNullOrWhiteSpace($rawPath)) {
                $DirToAdd
            } elseif ($rawPath.EndsWith(";")) {
                "$rawPath$DirToAdd"
            } else {
                "$rawPath;$DirToAdd"
            }
            $regKey.SetValue("Path", $newRawPath, [Microsoft.Win32.RegistryValueKind]::ExpandString)
            $addedPersistently = $true
        }
    } finally {
        if ($null -ne $regKey) {
            $regKey.Close()
        }
    }

    # Update current PowerShell session PATH immediately.
    $sessionSegments = @($env:Path -split ";" | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    $inSession = $false
    foreach ($seg in $sessionSegments) {
        if ($seg.Trim().TrimEnd("\").ToLowerInvariant() -eq $normTarget) {
            $inSession = $true
            break
        }
    }
    if (-not $inSession) {
        $env:Path = if ([string]::IsNullOrWhiteSpace($env:Path)) { $DirToAdd } else { "$DirToAdd;$env:Path" }
    }

    return $addedPersistently
}

$MirrorPrefix = Validate-Mirror $Mirror
$Arch = Get-VeriArch

$ResolvedTag = if ([string]::IsNullOrWhiteSpace($Version)) {
    Resolve-LatestVersion $GitHubBase
} else {
    Normalize-Version $Version
}
$NumVersion = $ResolvedTag.Substring(1)

if ([string]::IsNullOrWhiteSpace($InstallDir)) {
    $localAppData = $env:LOCALAPPDATA
    if ([string]::IsNullOrWhiteSpace($localAppData)) {
        $localAppData = Join-Path $HOME "AppData\Local"
    }
    $InstallDir = Join-Path $localAppData "veri\bin"
}

$Asset = "veri-$NumVersion-windows-$Arch.zip"
$OfficialReleaseBase = "$GitHubBase/releases/download/$ResolvedTag"
$OfficialAssetUrl = "$OfficialReleaseBase/$Asset"
$OfficialSumsUrl = "$OfficialReleaseBase/SHA256SUMS.txt"

$TempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("veri-install-" + [System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $TempDir | Out-Null

try {
    Write-Host "Installing veri $ResolvedTag (windows/$Arch)..."

    $sumsPath = Join-Path $TempDir "SHA256SUMS.txt"
    Download-File -Url $OfficialSumsUrl -DestPath $sumsPath

    $expectedHash = Parse-ExpectedSha256 -SumsPath $sumsPath -TargetAsset $Asset
    if ([string]::IsNullOrWhiteSpace($expectedHash)) {
        Fail "$Asset was not found with a valid SHA-256 entry in SHA256SUMS.txt for $ResolvedTag"
    }

    $archivePath = Join-Path $TempDir $Asset
    if (-not [string]::IsNullOrWhiteSpace($MirrorPrefix)) {
        $mirrorAssetUrl = "$MirrorPrefix$OfficialAssetUrl"
        try {
            Download-File -Url $mirrorAssetUrl -DestPath $archivePath
        } catch {
            Write-Host "Mirror download failed; falling back to official release URL..."
            Download-File -Url $OfficialAssetUrl -DestPath $archivePath
        }
    } else {
        Download-File -Url $OfficialAssetUrl -DestPath $archivePath
    }

    $actualHash = (Get-FileHash -Path $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actualHash -ne $expectedHash) {
        Fail "SHA-256 checksum mismatch for ${Asset}: expected $expectedHash, got $actualHash"
    }

    $extractedExe = Join-Path $TempDir $Binary
    Extract-RootBinaryFromZip -ZipPath $archivePath -BinaryName $Binary -DestPath $extractedExe

    $installedPath = Install-BinarySafely -SourceExe $extractedExe -TargetDir $InstallDir -BinaryName $Binary
    $pathUpdated = Add-ToUserPath -DirToAdd $InstallDir

    Write-Host "Installed veri $ResolvedTag to $installedPath"
    if ($pathUpdated) {
        Write-Host "Added $InstallDir to your user PATH (new terminals will pick it up automatically)."
    }
    Write-Host ""
    Write-Host "Next steps:"
    Write-Host "  veri version"
    Write-Host "  veri update"

    $userHome = if ($env:USERPROFILE) { $env:USERPROFILE } else { $HOME }
    $skillPath = if ($userHome) { Join-Path $userHome ".agents\skills\veri\SKILL.md" } else { "" }
    if ([string]::IsNullOrWhiteSpace($skillPath) -or -not (Test-Path -LiteralPath $skillPath -PathType Leaf)) {
        Write-Host ""
        Write-Host "Optional Agent Skill:"
        Write-Host "  npx skills add $Repo   (offline: veri skill install)"
    }
} finally {
    Remove-Item -LiteralPath $TempDir -Recurse -Force -ErrorAction SilentlyContinue
}

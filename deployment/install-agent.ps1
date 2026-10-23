param(
 [Parameter(Mandatory)][string]$PanelURL,
 [Parameter(Mandatory)][string]$ServerID,
 [Parameter(Mandatory)][string]$TokenFile,
 [string]$AgentExe=(Join-Path (Split-Path $PSScriptRoot -Parent) 'pulse-agent.exe'),
 [string]$StateDirectory='C:\ProgramData\PulseAgent'
)
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'windows-permissions.ps1')
if(-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)){throw 'Run PowerShell as Administrator'}
$agentPath=(Resolve-Path -LiteralPath $AgentExe).Path
$tokenPath=(Resolve-Path -LiteralPath $TokenFile).Path
$StateDirectory=[IO.Path]::GetFullPath($StateDirectory)
if($StateDirectory.TrimEnd('\') -eq [IO.Path]::GetPathRoot($StateDirectory).TrimEnd('\')){throw 'Install directory must not be a drive root'}
$ancestor=$StateDirectory
while($ancestor){
 if((Test-Path -LiteralPath $ancestor) -and ((Get-Item -LiteralPath $ancestor).Attributes -band [IO.FileAttributes]::ReparsePoint)){throw 'Install directory ancestors must not be links'}
 $ancestor=Split-Path $ancestor -Parent
}
foreach($argument in @($agentPath,$tokenPath,$StateDirectory,$PanelURL,$ServerID)){if($argument.Contains('"') -or $argument.Contains("`n") -or $argument.Contains("`r")){throw 'Arguments must not contain quotes or newlines'}}
$driveRoot=[IO.Path]::GetPathRoot($StateDirectory)
$volume=Get-Volume -DriveLetter $driveRoot.Substring(0,1) -ErrorAction Stop
if($volume.FileSystemType -ne 'NTFS'){throw 'Pulse Agent requires NTFS so its privileged executable, tokens and module state can be protected. Move an existing installation to NTFS before upgrading.'}
if((Get-Content -LiteralPath $tokenPath -Raw).Trim() -notmatch '^[0-9a-fA-F]{64}$'){throw 'Token must contain exactly 64 hexadecimal characters'}
if((Test-Path -LiteralPath $StateDirectory) -and (Get-ChildItem -LiteralPath $StateDirectory -Force | Select-Object -First 1)){Assert-PulsePrivateTree -Path $StateDirectory}
New-Item -ItemType Directory -Force $StateDirectory | Out-Null
$directoryAcl=[Security.AccessControl.DirectorySecurity]::new()
$directoryAcl.SetAccessRuleProtection($true,$false)
$directoryAcl.SetOwner([Security.Principal.WindowsIdentity]::GetCurrent().User)
foreach($sid in @('S-1-5-18','S-1-5-32-544')) {
 $directoryAcl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new($sid),[Security.AccessControl.FileSystemRights]::FullControl,([Security.AccessControl.InheritanceFlags]::ContainerInherit -bor [Security.AccessControl.InheritanceFlags]::ObjectInherit),[Security.AccessControl.PropagationFlags]::None,[Security.AccessControl.AccessControlType]::Allow))
}
Set-Acl -LiteralPath $StateDirectory -AclObject $directoryAcl -ErrorAction Stop
$service=Get-Service -Name PulseAgent -ErrorAction SilentlyContinue
if($service){Stop-Service PulseAgent; $service.WaitForStatus('Stopped',[TimeSpan]::FromSeconds(30))}
$installedExe=Join-Path $StateDirectory 'pulse-agent.exe'
$installedToken=Join-Path $StateDirectory 'agent.token'
if($agentPath -ne $installedExe){Copy-Item -LiteralPath $agentPath -Destination $installedExe -Force}
if($tokenPath -ne $installedToken){Copy-Item -LiteralPath $tokenPath -Destination $installedToken -Force}
$tokenAcl=[Security.AccessControl.FileSecurity]::new()
$tokenAcl.SetAccessRuleProtection($true,$false)
$tokenAcl.SetOwner([Security.Principal.WindowsIdentity]::GetCurrent().User)
foreach($sid in @('S-1-5-18','S-1-5-32-544')) {
 $tokenAcl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new($sid),[Security.AccessControl.FileSystemRights]::FullControl,[Security.AccessControl.AccessControlType]::Allow))
}
Set-Acl -LiteralPath $installedToken -AclObject $tokenAcl -ErrorAction Stop
$binary='"'+$installedExe+'" -url "'+$PanelURL+'" -server "'+$ServerID+'" -token-file "'+$installedToken+'" -state "'+$StateDirectory+'"'
if($service){& (Join-Path $env:SystemRoot 'System32/sc.exe') config PulseAgent binPath= $binary start= auto | Out-Null; if($LASTEXITCODE -ne 0){throw 'Service update failed'}}
else {New-Service -Name PulseAgent -DisplayName 'Pulse telemetry agent' -BinaryPathName $binary -StartupType Automatic | Out-Null}
& (Join-Path $env:SystemRoot 'System32/sc.exe') failure PulseAgent reset= 86400 actions= restart/5000/restart/15000/restart/60000 | Out-Null
if($LASTEXITCODE -ne 0){throw 'Service recovery configuration failed'}
Start-Service PulseAgent
Get-Service PulseAgent

# Refuse unsafe upgrades before touching an existing tree. A parent DACL
# cannot remove explicit child permissions or an untrusted owner.
function Assert-PulsePrivateTree {
 param([Parameter(Mandatory=$true)][string]$Path,[switch]$AllowLocalService)
 if(-not (Test-Path -LiteralPath $Path)){return}
 $allowed=@('S-1-5-18','S-1-5-32-544')
 if($AllowLocalService){$allowed+='S-1-5-19'}
 $owners=@('S-1-5-18','S-1-5-32-544',[Security.Principal.WindowsIdentity]::GetCurrent().User.Value)
 $pending=[Collections.Generic.Stack[string]]::new()
 $pending.Push([IO.Path]::GetFullPath($Path))
 while($pending.Count -gt 0){
  $item=Get-Item -LiteralPath $pending.Pop() -Force -ErrorAction Stop
  if($item.Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Existing installation contains a link. Migrate into a fresh private NTFS directory.'}
  $acl=Get-Acl -LiteralPath $item.FullName -ErrorAction Stop
  if($acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -notin $owners){throw 'Existing installation has an untrusted owner. Migrate into a fresh private NTFS directory.'}
  foreach($rule in $acl.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier])){
   if($rule.AccessControlType -eq [Security.AccessControl.AccessControlType]::Allow -and $rule.IdentityReference.Value -notin $allowed){throw 'Existing installation has public permissions. Migrate into a fresh private NTFS directory before upgrading.'}
  }
  # Validate each parent before listing its children; never traverse links.
  if($item.PSIsContainer){foreach($child in Get-ChildItem -LiteralPath $item.FullName -Force -ErrorAction Stop){$pending.Push($child.FullName)}}
 }
}

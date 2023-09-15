#Requires -Version 7

# OpenAPI doesn't allow enums for integers (`AsUncheckedInteger`), so `VSmbShareFlags` and
# `Plan9ShareFlags` get generated as strings, without the actual values.
#
# Current solution is override the fields as uint64 (see `config.json`), and then manually add
# look ups to go from the strings to the `AsUncheckedInteger` enum value.
    # "Types": {
    #     "Plan9ShareFlags": {
    #         "json-schema": {
    #             "TargetType": "integer",
    #             "TargetFormat": "uint64",
    #             "Format": "uint64"
    #         }
    #     }
    # },
$ErrorActionPreference = 'Stop'
$VerbosePreference = 'Continue'

function fail ( [string]$Message ) {
    # like throw, but without the obnoxious big red block
    Write-Error -ErrorAction 'Stop' $Message
}

function run {
    param(
        [Parameter(Mandatory, Position = 0)]
        [string]$cmd,

        [Parameter(Position = 1, ValueFromRemainingArguments)]
        [string[]]$params
    )

    Write-Verbose "$cmd $params"
    & $cmd @params 2>&1
    if ( $LASTEXITCODE -ne 0 ) {
        fail "Command failed: $cmd $params"
    }
}

$root = run git 'rev-parse' '--show-toplevel'

$destDir = Join-Path $root 'out/hcsschema'
New-Item -ItemType Directory -Force -Path $destDir > $null
$schema = Join-Path $destDir 'schema.json'

$osDir = 'E:\src\os'
$marsSchemaDir = Join-Path $osDir 'src\onecore\vm\compute\schema'
if ( -not (Test-Path -PathType Container $marsSchemaDir) ) {
    fail "Non-existant schema directory: $marsSchemaDir"
}

$configs = @(
    (Join-Path $osDir 'src\data\MarsComp\config_base.json'),
    (Join-Path $osDir 'src\onecore\vm\schema\config.json')
) |
    ForEach-Object { '/config:' +  $_ }

$marsFiles = Get-ChildItem -Path $marsSchemaDir -Name -File -Include '*.mars' |
    ForEach-Object { '/f:' + (Join-Path $marsSchemaDir $_) }

$marsCompDir = Join-Path $root 'bin\tool\marscomp'
$marsComp = Join-Path $marsCompDir 'x86\MarsComp.exe'
if ( -not (Test-Path -PathType Leaf $marsComp) ) {
    Write-Warning "Downloading 'marscomp.exe' to: $marsCompDir"
    New-Item -ItemType Directory -Force -Path $marsCompDir > $null

    # https://www.osgwiki.com/wiki/How_to_pull_vPacks_produced_from_OSGTools_from_VSO-Drop
    run '\\winbuilds\buildfile\osgtools\vpack\vpack.exe' pull /n:OSGTools.main.MarsComp '/ver:[0.11.0,)' /pr:Continuous "/destdir:$marsCompDir"
}

run $marsComp @configs @marsFiles "/I:$marsSchemaDir" "/out_jschema:$schema" /remove_namespace:true

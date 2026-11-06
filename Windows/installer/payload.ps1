# Single inventory shared by production preflight and explicit compile evidence.
$NativePayload = @('BlueGrass.Native.exe','BlueGrass.Service.exe','BlueGrass.Repair.exe','BlueGrass.Update.exe','BlueGrass.Diagnostics.exe')
$RequiredPayload = @('BlueGrass.exe') + $NativePayload + @('flutter_windows.dll','wintun.dll','LICENSE.Xray-core','LICENSE.wintun','LICENSE.sing','data\icudtl.dat','data\app.so')
$RoutingDataPayload = @(
    'geoip-cn-v4.dat', 'geosite-cn-foreign.dat', 'metadata.json', 'NOTICE.md',
    'licenses\LICENSE-DB-IP-CC-BY-4.0.txt', 'licenses\LICENSE-geoip-CC-BY-SA-4.0.txt',
    'licenses\LICENSE-geosite-MIT.txt', 'licenses\LICENSE-Xray-MPL-2.0.txt'
)
$RequiredPayload += $RoutingDataPayload | ForEach-Object { 'data\routing-policy\' + $_ }
$OptionalPayload = @(
    'BlueGrass.SetupTrust.exe','BlueGrass.UserCleanup.exe',
    'concrt140.dll','msvcp140.dll','msvcp140_1.dll','msvcp140_2.dll',
    'msvcp140_atomic_wait.dll','msvcp140_codecvt_ids.dll','vccorlib140.dll','vcruntime140.dll',
    'vcruntime140_1.dll','vcruntime140_threads.dll',
    'data\flutter_assets\FontManifest.json','data\flutter_assets\AssetManifest.bin',
    'data\flutter_assets\NativeAssetsManifest.json','data\flutter_assets\NOTICES.Z',
    'data\flutter_assets\fonts\MaterialIcons-Regular.otf',
    'data\flutter_assets\assets\branding\wordmark-light.png',
    'data\flutter_assets\assets\branding\wordmark-dark.png',
    'data\flutter_assets\assets\branding\4.0x\wordmark-light.png',
    'data\flutter_assets\assets\branding\4.0x\wordmark-dark.png',
    'data\flutter_assets\shaders\ink_sparkle.frag','data\flutter_assets\shaders\stretch_effect.frag',
    'data\flutter_assets\assets\LICENSE.Xray-core',
    'data\flutter_assets\assets\LICENSE.wintun',
    'data\flutter_assets\assets\LICENSE.sing'
)

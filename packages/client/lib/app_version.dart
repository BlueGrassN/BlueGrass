const clientVersion = String.fromEnvironment(
  'BLUEGRASS_VERSION',
  defaultValue: '0.1.0',
);
const clientBuild = int.fromEnvironment('BLUEGRASS_BUILD', defaultValue: 1);
// Diagnostics retain the internal build and historical version-label format.
// Public UI must use clientVersion, injected from root release.json by builds.
const clientVersionLabel = '$clientVersion+$clientBuild';

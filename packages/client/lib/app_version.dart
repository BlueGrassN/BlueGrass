const clientVersion = String.fromEnvironment(
  'BLUEGRASS_VERSION',
  defaultValue: '0.1.0',
);
const clientBuild = int.fromEnvironment('BLUEGRASS_BUILD', defaultValue: 1);
const clientVersionLabel = '$clientVersion+$clientBuild';

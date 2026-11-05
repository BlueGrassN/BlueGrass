// The launcher deliberately imports the production desktop entry point.
// No fixture entry point or copied screens belong in this app.
import 'package:bluegrass_client/main.dart' as desktop;

export 'package:bluegrass_client/main.dart' show MyApp;

void main() => desktop.main();

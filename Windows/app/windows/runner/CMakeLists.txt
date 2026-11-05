cmake_minimum_required(VERSION 3.14)
project(runner LANGUAGES CXX)

# Define the application target. To change its name, change BINARY_NAME in the
# top-level CMakeLists.txt, not the value here, or `flutter run` will no longer
# work.
#
# Any new source files that you add to the application should be added here.
add_executable(${BINARY_NAME} WIN32
  "bluegrass_platform.cpp"
  "flutter_window.cpp"
  "main.cpp"
  "utils.cpp"
  "win32_window.cpp"
  "${FLUTTER_MANAGED_DIR}/generated_plugin_registrant.cc"
  "Runner.rc"
  "runner.exe.manifest"
)

# Apply the standard set of build settings. This can be removed for applications
# that need different build settings.
apply_standard_settings(${BINARY_NAME})
target_compile_options(${BINARY_NAME} PRIVATE /utf-8)

# Add preprocessor definitions for the build version.
target_compile_definitions(${BINARY_NAME} PRIVATE "FLUTTER_VERSION=\"${FLUTTER_VERSION}\"")
target_compile_definitions(${BINARY_NAME} PRIVATE "FLUTTER_VERSION_MAJOR=${FLUTTER_VERSION_MAJOR}")
target_compile_definitions(${BINARY_NAME} PRIVATE "FLUTTER_VERSION_MINOR=${FLUTTER_VERSION_MINOR}")
target_compile_definitions(${BINARY_NAME} PRIVATE "FLUTTER_VERSION_PATCH=${FLUTTER_VERSION_PATCH}")
target_compile_definitions(${BINARY_NAME} PRIVATE "FLUTTER_VERSION_BUILD=${FLUTTER_VERSION_BUILD}")

# Disable Windows macros that collide with C++ standard library functions.
target_compile_definitions(${BINARY_NAME} PRIVATE "NOMINMAX")

# Add dependency libraries and include directories. Add any application-specific
# dependencies here.
target_link_libraries(${BINARY_NAME} PRIVATE flutter flutter_wrapper_app)
target_link_libraries(${BINARY_NAME} PRIVATE "dwmapi.lib")
target_link_libraries(${BINARY_NAME} PRIVATE "crypt32.lib" "shell32.lib" "ole32.lib" "advapi32.lib")
# Keep C++/WinRT exception support inside this library; Flutter's runner uses
# _HAS_EXCEPTIONS=0. The bridge header deliberately exposes no WinRT types.
add_library(bluegrass_notifications STATIC "desktop_notifications.cpp")
target_compile_features(bluegrass_notifications PUBLIC cxx_std_17)
target_compile_options(bluegrass_notifications PRIVATE /W4 /WX /wd4100 /EHsc /utf-8)
target_compile_definitions(bluegrass_notifications PRIVATE NOMINMAX)
target_link_libraries(bluegrass_notifications PUBLIC "windowsapp.lib" "ole32.lib"
  "shell32.lib" "advapi32.lib" "propsys.lib" "bcrypt.lib")
target_link_libraries(${BINARY_NAME} PRIVATE bluegrass_notifications)
add_library(bluegrass_control_http STATIC
  "control_http.cpp" "control_http_protocol.cpp" "control_http_route.cpp"
  "control_http_bridge.cpp")
target_compile_features(bluegrass_control_http PUBLIC cxx_std_17)
target_compile_options(bluegrass_control_http PRIVATE /W4 /WX /wd4100 /EHsc /utf-8)
target_compile_definitions(bluegrass_control_http PRIVATE NOMINMAX WIN32_LEAN_AND_MEAN
  _WIN32_WINNT=0x0A00)
target_link_libraries(bluegrass_control_http PUBLIC flutter flutter_wrapper_app
  "ws2_32.lib" "iphlpapi.lib" "secur32.lib" "crypt32.lib")
target_link_libraries(${BINARY_NAME} PRIVATE bluegrass_control_http)
target_include_directories(${BINARY_NAME} PRIVATE "${CMAKE_SOURCE_DIR}")

# Run the Flutter tool portions of the build. This must not be removed.
add_dependencies(${BINARY_NAME} flutter_assemble)

# Portable ZIP entry point. Keep this output out of the ordinary Flutter/installer
# payload; packaging places it above runtime under the public BlueGrass.exe name.
add_executable(BlueGrassLauncher WIN32 "portable_launcher.cpp" "Runner.rc"
  "runner.exe.manifest")
apply_standard_settings(BlueGrassLauncher)
target_compile_options(BlueGrassLauncher PRIVATE /utf-8)
target_compile_definitions(BlueGrassLauncher PRIVATE NOMINMAX
  "FLUTTER_VERSION=\"${FLUTTER_VERSION}\""
  "FLUTTER_VERSION_MAJOR=${FLUTTER_VERSION_MAJOR}"
  "FLUTTER_VERSION_MINOR=${FLUTTER_VERSION_MINOR}"
  "FLUTTER_VERSION_PATCH=${FLUTTER_VERSION_PATCH}"
  "FLUTTER_VERSION_BUILD=${FLUTTER_VERSION_BUILD}")
set_target_properties(BlueGrassLauncher PROPERTIES
  MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>"
  RUNTIME_OUTPUT_DIRECTORY "${CMAKE_SOURCE_DIR}/../../.artifacts/launcher")

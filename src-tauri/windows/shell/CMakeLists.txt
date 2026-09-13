cmake_minimum_required(VERSION 3.20)
project(freeace_shell LANGUAGES CXX RC)

add_library(freeace_shell SHARED
  dllmain.cpp
  freeace_shell.rc
)

add_library(freeace_extract_shell SHARED
  dllmain.cpp
  freeace_extract_shell.rc
)

target_compile_features(freeace_shell PRIVATE cxx_std_17)
target_compile_definitions(freeace_shell PRIVATE UNICODE _UNICODE WIN32_LEAN_AND_MEAN NOMINMAX)
target_link_libraries(freeace_shell PRIVATE shlwapi ole32 shell32)
target_compile_features(freeace_extract_shell PRIVATE cxx_std_17)
target_compile_definitions(freeace_extract_shell PRIVATE UNICODE _UNICODE WIN32_LEAN_AND_MEAN NOMINMAX)
target_link_libraries(freeace_extract_shell PRIVATE shlwapi ole32 shell32)
set_target_properties(freeace_shell PROPERTIES
  OUTPUT_NAME "freeace_shell"
  RUNTIME_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}/out"
  LIBRARY_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}/out"
  ARCHIVE_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}/out"
)
set_target_properties(freeace_extract_shell PROPERTIES
  OUTPUT_NAME "freeace_extract_shell"
  RUNTIME_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}/out"
  LIBRARY_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}/out"
  ARCHIVE_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}/out"
)

if(MSVC)
  set_property(TARGET freeace_shell PROPERTY
    MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")
  set_property(TARGET freeace_extract_shell PROPERTY
    MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")
  target_link_options(freeace_shell PRIVATE "/DEF:${CMAKE_CURRENT_SOURCE_DIR}/freeace_shell.def")
  target_link_options(freeace_extract_shell PRIVATE "/DEF:${CMAKE_CURRENT_SOURCE_DIR}/freeace_extract_shell.def")
endif()

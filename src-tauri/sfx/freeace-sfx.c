/* FreeAce self-extracting stub.
 *
 * SPDX-License-Identifier: GPL-3.0-or-later
 *
 * A FreeAce SFX is this program with a complete .freeace archive appended.
 * Gleipnir's arc_open finds the appended archive by its self-checksummed
 * header (see find_sfx_base), so extraction is plain `gleipnir x` pointed at
 * this executable. The whole engine is compiled in; main is renamed so this
 * file can supply the interactive front end.
 *
 * usage: <name>.exe [-o<folder>] [-y]
 *   default folder: <name> beside the executable
 *   -y            no prompts (for scripts)
 */

#define main gleipnir_main
#include "../../third_party/gleipnir/gleipnir.c"
#undef main

#ifdef _WIN32
#include <direct.h>
#include <sys/stat.h>
#define SFX_MKDIR(p) _mkdir(p)
#else
#include <sys/stat.h>
#include <unistd.h>
#define SFX_MKDIR(p) mkdir((p), 0777)
#endif
#ifdef __APPLE__
#include <mach-o/dyld.h>
#endif

#define SFX_PATH_MAX 4096

static int self_path(char *out, size_t cap, const char *argv0) {
#ifdef _WIN32
    /* The ANSI form, because Gleipnir opens files with narrow fopen; a path
     * the code page cannot express comes back with '?' and is refused. */
    (void)argv0;
    DWORD n = GetModuleFileNameA(NULL, out, (DWORD)cap);
    if (n == 0 || n >= cap || strchr(out, '?')) return -1;
    return 0;
#elif defined(__APPLE__)
    uint32_t size = (uint32_t)cap;
    return _NSGetExecutablePath(out, &size) == 0 ? 0 : -1;
#else
    ssize_t n = readlink("/proc/self/exe", out, cap - 1);
    if (n > 0) { out[n] = 0; return 0; }
    if (!argv0 || strlen(argv0) >= cap) return -1;
    strcpy(out, argv0);
    return 0;
#endif
}

/* "C:\dl\photos.exe" -> "C:\dl\photos"; a name with no extension gets
 * "_extracted" so the folder never collides with the executable itself. */
static void default_destination(const char *self, char *out, size_t cap) {
    snprintf(out, cap, "%s", self);
    char *slash = strrchr(out, '/');
#ifdef _WIN32
    char *back = strrchr(out, '\\');
    if (!slash || (back && back > slash)) slash = back;
#endif
    char *dot = strrchr(out, '.');
    if (dot && (!slash || dot > slash)) *dot = 0;
    else if (strlen(out) + 11 < cap) strcat(out, "_extracted");
}

static void read_line(char *buf, size_t cap) {
    if (!fgets(buf, (int)cap, stdin)) { buf[0] = 0; return; }
    size_t n = strlen(buf);
    while (n && (buf[n - 1] == '\n' || buf[n - 1] == '\r')) buf[--n] = 0;
}

static int path_exists(const char *path) {
    struct stat st;
    return stat(path, &st) == 0;
}

/* Gleipnir creates every folder below the destination but not the
 * destination itself. */
static void make_dirs(const char *path) {
    char tmp[SFX_PATH_MAX];
    snprintf(tmp, sizeof tmp, "%s", path);
    for (char *p = tmp + 1; *p; p++) {
        if (*p != '/' && *p != '\\') continue;
        char c = *p; *p = 0;
        if (!(p == tmp + 2 && tmp[1] == ':')) SFX_MKDIR(tmp);
        *p = c;
    }
    SFX_MKDIR(tmp);
}

static int finish(int rc, int interactive, const char *message) {
    if (interactive) {
        char line[8];
        fprintf(stderr, "\n%s Press Enter to close.\n", message);
        read_line(line, sizeof line);
    }
    return rc;
}

int main(int argc, char **argv) {
    char self[SFX_PATH_MAX], dest[SFX_PATH_MAX];
    int interactive = 1;
    dest[0] = 0;
    for (int i = 1; i < argc; i++) {
        if (!strcmp(argv[i], "-y")) interactive = 0;
        else if (!strncmp(argv[i], "-o", 2) && argv[i][2])
            snprintf(dest, sizeof dest, "%s", argv[i] + 2);
        else {
            fprintf(stderr, "usage: %s [-o<folder>] [-y]\n", argv[0]);
            return 1;
        }
    }
    if (self_path(self, sizeof self, argv[0]) != 0) {
        fprintf(stderr, "freeace-sfx: cannot locate this program's own file. "
                        "Move it to a folder whose path uses only plain "
                        "characters and run it again.\n");
        return finish(1, interactive, "Extraction failed.");
    }
    /* Check the payload before prompting or creating anything. */
    Archive probe;
    int opened = arc_open(self, &probe);
    arc_close(&probe);
    if (opened != 0) {
        fprintf(stderr, "freeace-sfx: this program does not contain a readable "
                        "FreeAce archive.\n");
        return finish(2, interactive, "Extraction failed.");
    }
    if (!dest[0]) default_destination(self, dest, sizeof dest);

    /* Every SFX carries this engine as object code, so it keeps the notice
     * gleipnir --version prints for the same reason (GPL section 5). */
    printf("FreeAce self-extracting archive\n"
           "Extractor: Gleipnir %s, Copyright (C) 2026 ValisSowilo. Free "
           "software under GPL-3.0-or-later\nwith NO WARRANTY; source: "
           "https://github.com/ValisSowilo and "
           "https://github.com/skonester/FreeAce\n\n", GLEIPNIR_RELEASE);
    if (interactive) {
        char line[SFX_PATH_MAX];
        printf("Extract to: %s\n"
               "Press Enter to continue, type another folder, or close this "
               "window to cancel.\n> ", dest);
        fflush(stdout);
        read_line(line, sizeof line);
        if (line[0]) snprintf(dest, sizeof dest, "%s", line);
        if (path_exists(dest)) {
            printf("%s already exists. Files with the same names will be "
                   "replaced. Continue? [y/N] ", dest);
            fflush(stdout);
            read_line(line, sizeof line);
            if (line[0] != 'y' && line[0] != 'Y')
                return finish(1, interactive, "Cancelled; nothing was extracted.");
        }
    }
    make_dirs(dest);
    printf("Extracting to %s\n", dest);
    fflush(stdout);

    char *gargv[] = { "gleipnir", "x", "-t0", self, dest, NULL };
    int rc = gleipnir_main(5, gargv);
    return finish(rc, interactive, rc == 0 ? "Done." : "Extraction failed.");
}

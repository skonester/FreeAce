import { invoke } from "@tauri-apps/api/core";
import { open } from "@tauri-apps/plugin-dialog";
import { showToast } from "./toast";
import { log } from "./ui";

export interface DiskImageMount {
  id: number;
  image: string;
  mountPoint: string | null;
}

interface DiskImageError {
  stage: string;
  message: string;
  osCode: number | null;
}

function errorMessage(error: unknown): string {
  if (typeof error === "object" && error !== null && "message" in error) {
    const detail = error as DiskImageError;
    return detail.message;
  }
  return String(error);
}

export function reportMountedImage(mount: DiskImageMount): void {
  const message = mount.mountPoint
    ? `Mounted disk image at ${mount.mountPoint}`
    : "Disk image attached. Windows is assigning a drive letter.";
  log(message);
  showToast(message, "success");
}

export async function mountRequestedImages(paths: string[]): Promise<void> {
  for (const path of paths) {
    try {
      reportMountedImage(
        await invoke<DiskImageMount>("mount_disk_image", { path }),
      );
    } catch (error) {
      const message = errorMessage(error);
      log(message, "error");
      showToast(message, "error");
    }
  }
}

export function initDiskImages(platform: string): void {
  if (platform !== "windows") return;
  const trigger =
    document.querySelector<HTMLButtonElement>("#open-disk-images");
  const dialog = document.querySelector<HTMLDialogElement>("#disk-images");
  const list = document.querySelector<HTMLUListElement>("#disk-images-list");
  const status = document.getElementById("disk-images-status");
  const picker = document.querySelector<HTMLButtonElement>("#mount-disk-image");
  if (!trigger || !dialog || !list || !status || !picker) return;

  let polling: number | undefined;
  let busy = false;
  let refreshing = false;
  let rendered = "";
  trigger.hidden = false;
  document
    .querySelectorAll<HTMLElement>("[data-disk-images-only]")
    .forEach((element) => {
      element.hidden = false;
    });

  async function refresh(): Promise<void> {
    if (!dialog!.open || busy || refreshing) return;
    refreshing = true;
    try {
      const mounts = await invoke<DiskImageMount[]>("list_disk_images");
      if (!dialog!.open || busy) return;
      const snapshot = JSON.stringify(mounts);
      if (snapshot === rendered) return;
      rendered = snapshot;
      list!.replaceChildren();
      if (!mounts.length) {
        const item = document.createElement("li");
        item.textContent = "No disk images mounted.";
        list!.append(item);
      }
      for (const mount of mounts) {
        const item = document.createElement("li");
        const label = document.createElement("span");
        label.textContent = `${mount.mountPoint ?? "Assigning drive letter"} • ${mount.image}`;
        const button = document.createElement("button");
        button.className = "btn btn--sm";
        button.textContent = "Unmount";
        button.addEventListener("click", () => {
          void action("Unmounting image…", async () => {
            await invoke("unmount_disk_image", { id: mount.id });
            log(`Unmounted ${mount.image}`);
          });
        });
        item.append(label, button);
        list!.append(item);
      }
    } catch (error) {
      status!.textContent = errorMessage(error);
    } finally {
      refreshing = false;
    }
  }

  async function action(
    message: string,
    run: () => Promise<void>,
  ): Promise<void> {
    if (busy) return;
    busy = true;
    picker!.disabled = true;
    status!.textContent = message;
    try {
      await run();
      status!.textContent = "";
    } catch (error) {
      status!.textContent = errorMessage(error);
      log(status!.textContent, "error");
    } finally {
      busy = false;
      picker!.disabled = false;
      await refresh();
    }
  }

  picker.addEventListener("click", () => {
    void action("Mounting image…", async () => {
      const path = await open({
        multiple: false,
        filters: [
          {
            name: "Disk images",
            extensions: [
              "iso",
              "cue",
              "bin",
              "img",
              "raw",
              "nrg",
              "mds",
              "mdf",
              "ccd",
            ],
          },
        ],
      });
      if (typeof path === "string") {
        reportMountedImage(
          await invoke<DiskImageMount>("mount_disk_image", { path }),
        );
      }
    });
  });

  trigger.addEventListener("click", () => {
    dialog.showModal();
    void refresh();
    polling = window.setInterval(() => void refresh(), 1000);
  });
  dialog.addEventListener("close", () => window.clearInterval(polling));
}

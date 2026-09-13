/**
 * Hide the webview's native right-click menu (WebView2 Back/Reload/Inspect,
 * WKWebView/WebKitGTK Reload) unless hidden debug mode turns it back on.
 *
 * A matching Tauri initialization script installs the same listener before
 * page JS so the native menu cannot flash during load.
 */

declare global {
  interface Window {
    __FREEACE_NATIVE_CONTEXT_MENU_GUARD__?: boolean;
    __FREEACE_ALLOW_NATIVE_CONTEXT_MENU__?: boolean;
  }
}

export function setNativeWebviewContextMenuAllowed(allowed: boolean): void {
  window.__FREEACE_ALLOW_NATIVE_CONTEXT_MENU__ = allowed;
}

export function isNativeWebviewContextMenuAllowed(): boolean {
  return window.__FREEACE_ALLOW_NATIVE_CONTEXT_MENU__ === true;
}

export function installNativeWebviewContextMenuGuard(): void {
  if (window.__FREEACE_NATIVE_CONTEXT_MENU_GUARD__) return;
  window.__FREEACE_NATIVE_CONTEXT_MENU_GUARD__ = true;
  document.addEventListener(
    "contextmenu",
    (event) => {
      if (!window.__FREEACE_ALLOW_NATIVE_CONTEXT_MENU__) {
        event.preventDefault();
      }
    },
    true,
  );
}

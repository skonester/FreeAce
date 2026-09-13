/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_FREEACE_E2E?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

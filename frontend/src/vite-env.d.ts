/// <reference types="svelte" />
/// <reference types="vite/client" />

declare module "*?raw" {
  const content: string;
  export default content;
}

declare module "virtual:frame-script" {
  const script: string;
  export default script;
}

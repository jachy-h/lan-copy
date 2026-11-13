// Lets plain TypeScript tooling (tsserver, tsc) resolve single-file components.
// vue-tsc still reads the real SFC types, so props and emits stay checked.
declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<Record<string, unknown>, Record<string, unknown>, unknown>
  export default component
}

// A TypeScript handler. esbuild transpiles it to ES2020 CommonJS, then goja
// runs it in-process on a goroutine — no Node, no V8, no cgo.
interface Greeting {
  hello: string;
  lang: string;
}

export function greet(payload: string): string {
  const d: { name?: string } = JSON.parse(payload);
  const out: Greeting = { hello: d.name ?? "world", lang: "typescript" };
  return JSON.stringify(out);
}

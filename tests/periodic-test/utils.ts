export function log(...msg: unknown[]) {
  console.log(new Date().toISOString(), ...msg);
}

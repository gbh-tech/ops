// Text module declaration for the bundled, base64-encoded install script.
// Bundling is driven by the "[[rules]] type = 'Text'" block in wrangler.toml.
declare module "*.b64" {
  const content: string;
  export default content;
}

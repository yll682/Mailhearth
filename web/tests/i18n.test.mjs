import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
// TypeScript 7 has no stable JS compiler API; Vite's bundled oxc parser
// reads TS/TSX into an ESTree AST.
import { parseSync } from "vite";

const root = fileURLToPath(new URL("../src/", import.meta.url));
const dictionaries = { zhCN: new Map(), zhTW: new Map(), ja: new Map(), es: new Map() };

function parse(filename) {
  const text = fs.readFileSync(filename, "utf8");
  const result = parseSync(filename, text);
  assert.deepEqual(result.errors, [], filename);
  return { text, program: result.program };
}
// forEachChild visits every AST node below node.
function forEachChild(node, fn) {
  for (const value of Object.values(node)) {
    if (Array.isArray(value)) { for (const item of value) if (item && typeof item.type === "string") fn(item); }
    else if (value && typeof value.type === "string") fn(value);
  }
}
const isString = (node) => node?.type === "Literal" && typeof node.value === "string";
const keyName = (key) => key.type === "Identifier" ? key.name : key.value;

const i18n = parse(path.join(root, "lib/i18n.ts"));
function collect(dictionary, object) {
  assert.equal(object?.type, "ObjectExpression");
  for (const property of object.properties) {
    assert.ok(property.type === "Property" && isString(property.value), "dictionary entries must be string literals");
    dictionary.set(keyName(property.key), property.value.value);
  }
}
function visitDictionary(node) {
  if (node.type === "VariableDeclarator" && node.id.type === "Identifier" && dictionaries[node.id.name]) collect(dictionaries[node.id.name], node.init);
  if (node.type === "CallExpression" && i18n.text.slice(node.callee.start, node.callee.end) === "Object.assign") {
    const target = i18n.text.slice(node.arguments[0].start, node.arguments[0].end);
    const name = { 'dicts["zh-CN"]': "zhCN", 'dicts["zh-TW"]': "zhTW", "dicts.ja": "ja", "dicts.es": "es" }[target] ?? target;
    if (dictionaries[name]) collect(dictionaries[name], node.arguments[1]);
  }
  forEachChild(node, visitDictionary);
}
visitDictionary(i18n.program);
const keys = new Set();
const untranslated = [];
function literals(node) {
  if (isString(node)) keys.add(node.value);
  if (node.type === "ConditionalExpression") { literals(node.consequent); literals(node.alternate); }
}
const han = /\p{Script=Han}/u;
function walk(directory) {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const filename = path.join(directory, entry.name);
    if (entry.isDirectory()) { walk(filename); continue; }
    if (!/\.tsx?$/.test(filename)) continue;
    const labels = filename.endsWith("resourceLabels.ts") || filename.endsWith("errorLabels.ts");
    function visit(node) {
      if (node.type === "JSXText" && han.test(node.value)) untranslated.push(`${filename}: ${node.value.trim()}`);
      if (node.type === "JSXAttribute" && isString(node.value) && han.test(node.value.value)) untranslated.push(`${filename}: ${node.value.value.trim()}`);
      if (node.type === "CallExpression" && node.callee.type === "Identifier" && node.callee.name === "t" && node.arguments.length) literals(node.arguments[0]);
      if (labels && node.type === "Property" && isString(node.value)) keys.add(node.value.value);
      forEachChild(node, visit);
    }
    visit(parse(filename).program);
  }
}
walk(root);
for (const dictionary of Object.values(dictionaries)) for (const key of dictionary.keys()) keys.add(key);

test("四种翻译覆盖全部静态界面文案和相同词典内容", () => {
  const missing = [];
  for (const [language, dictionary] of Object.entries(dictionaries)) for (const key of keys) if (!dictionary.get(key)?.trim()) missing.push(`${language}: ${key}`);
  assert.deepEqual(missing, []);
});
test("翻译保留全部插值参数", () => {
  const placeholders = (value) => [...value.matchAll(/\{([A-Za-z][A-Za-z0-9_]*)\}/g)].map((match) => match[1]).sort();
  for (const [language, dictionary] of Object.entries(dictionaries)) for (const [key, value] of dictionary) assert.deepEqual(placeholders(value), placeholders(key), `${language}: ${key}`);
});
test("界面中文文本使用语言词典", () => {
  assert.deepEqual(untranslated, []);
});

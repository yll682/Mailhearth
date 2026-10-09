import assert from "node:assert/strict";
import test from "node:test";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";

const root = fileURLToPath(new URL("../src/", import.meta.url));
const dictionaries = { zhCN: new Map(), zhTW: new Map(), ja: new Map(), es: new Map() };
const source = ts.createSourceFile("i18n.ts", fs.readFileSync(path.join(root, "lib/i18n.ts"), "utf8"), ts.ScriptTarget.Latest, true);
function collect(dictionary, object) {
  assert.ok(ts.isObjectLiteralExpression(object));
  for (const property of object.properties) {
    assert.ok(ts.isPropertyAssignment(property) && ts.isStringLiteral(property.initializer));
    dictionary.set(property.name.text, property.initializer.text);
  }
}
function visitDictionary(node) {
  if (ts.isVariableDeclaration(node) && dictionaries[node.name.text]) collect(dictionaries[node.name.text], node.initializer);
  if (ts.isCallExpression(node) && node.expression.getText(source) === "Object.assign") {
    const target = node.arguments[0].getText(source);
    const name = { 'dicts["zh-CN"]': "zhCN", 'dicts["zh-TW"]': "zhTW", "dicts.ja": "ja", "dicts.es": "es" }[target] ?? target;
    if (dictionaries[name]) collect(dictionaries[name], node.arguments[1]);
  }
  ts.forEachChild(node, visitDictionary);
}
visitDictionary(source);
const keys = new Set();
const untranslated = [];
function literals(node) {
  if (ts.isStringLiteral(node)) keys.add(node.text);
  if (ts.isConditionalExpression(node)) { literals(node.whenTrue); literals(node.whenFalse); }
}
function walk(directory) {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const filename = path.join(directory, entry.name);
    if (entry.isDirectory()) { walk(filename); continue; }
    if (!/\.tsx?$/.test(filename)) continue;
    const file = ts.createSourceFile(filename, fs.readFileSync(filename, "utf8"), ts.ScriptTarget.Latest, true, filename.endsWith("tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
    function visit(node) {
      if ((ts.isJsxText(node) || ts.isStringLiteral(node) && ts.isJsxAttribute(node.parent)) && /\p{Script=Han}/u.test(node.text)) untranslated.push(`${filename}: ${node.text.trim()}`);
      if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === "t" && node.arguments.length) literals(node.arguments[0]);
      if ((filename.endsWith("resourceLabels.ts") || filename.endsWith("errorLabels.ts")) && ts.isPropertyAssignment(node) && ts.isStringLiteral(node.initializer)) keys.add(node.initializer.text);
      ts.forEachChild(node, visit);
    }
    visit(file);
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

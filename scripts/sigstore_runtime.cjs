"use strict";

const { execFileSync } = require("node:child_process");
const path = require("node:path");

function npmModule(name) {
  const globalRoot = execFileSync("npm", ["root", "-g"], { encoding: "utf8" }).trim();
  const npmPackage = require.resolve("npm/package.json", { paths: [globalRoot] });
  const resolved = require.resolve(name, { paths: [path.dirname(npmPackage)] });
  return require(resolved);
}

module.exports = { npmModule };

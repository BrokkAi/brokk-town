"use strict";

const crypto = require("node:crypto");
const { npmModule } = require("./sigstore_runtime.cjs");

const REPO = "BrokkAi/brokk-town";
const WORKFLOW = "publish-packages.yml";
const ISSUER = "https://token.actions.githubusercontent.com";
const INTOTO_TYPE = "application/vnd.in-toto+json";

function required(name) {
  const value = process.env[name];
  if (!value) {
    throw new Error(`missing ${name}`);
  }
  return value;
}

function certificatePolicy(values) {
  // Fulcio retains the GitHub-specific OIDs for compatibility. They are raw
  // string extensions, unlike the DER-encoded generic V2 extensions.
  return {
    "1.3.6.1.4.1.57264.1.1": values.issuer,
    "1.3.6.1.4.1.57264.1.3": values.sha,
    "1.3.6.1.4.1.57264.1.5": values.repository,
    "1.3.6.1.4.1.57264.1.6": values.ref,
  };
}

async function main() {
  const values = {
    issuer: ISSUER,
    sha: required("GITHUB_SHA"),
    workflow: required("GITHUB_WORKFLOW"),
    repository: required("GITHUB_REPOSITORY"),
    ref: required("GITHUB_REF"),
  };
  if (values.repository !== REPO || values.workflow !== "Publish packages") {
    throw new Error("unexpected signing context");
  }

  const run = [required("GITHUB_RUN_ID"), required("GITHUB_RUN_ATTEMPT")].join(":");
  const statement = {
    _type: "https://in-toto.io/Statement/v1",
    subject: [{
      name: "pkg:generic/brokk-town-sigstore-preflight",
      digest: { sha256: crypto.createHash("sha256").update(run).digest("hex") },
    }],
    predicateType: "https://brokk.ai/release/sigstore-preflight/v1",
    predicate: {
      repository: REPO,
      commit: values.sha,
      ref: values.ref,
      workflow: `.github/workflows/${WORKFLOW}`,
      run,
    },
  };

  const sigstore = npmModule("sigstore");
  const bundle = await sigstore.attest(Buffer.from(JSON.stringify(statement)), INTOTO_TYPE);
  const entries = bundle.verificationMaterial?.tlogEntries;
  if (!Array.isArray(entries) || entries.length !== 1) {
    throw new Error("Fulcio preflight did not produce one Rekor entry");
  }

  // This independently reloads trust material, verifies the Fulcio chain and
  // SCT, checks the DSSE signature, validates the Rekor inclusion proof/body,
  // and binds the certificate to the exact GitHub signing identity.
  await sigstore.verify(bundle, {
    certificateOIDs: certificatePolicy(values),
    certificateIdentityURI: `^https://github\\.com/${REPO}/\\.github/workflows/${WORKFLOW}@${values.ref.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`,
  });
  process.stdout.write(JSON.stringify({
    fulcio: true,
    rekor: true,
    logIndex: entries[0].logIndex,
    integratedTime: entries[0].integratedTime,
  }) + "\n");
}

main().catch(() => process.exit(1));

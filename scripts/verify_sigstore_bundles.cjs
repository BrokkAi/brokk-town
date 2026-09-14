"use strict";

const fs = require("node:fs/promises");
const { npmModule } = require("./sigstore_runtime.cjs");

const REPO = "BrokkAi/brokk-town";
const WORKFLOW = "publish-packages.yml";
const ISSUER = "https://token.actions.githubusercontent.com";
const SLSA_V1 = "https://slsa.dev/provenance/v1";
const BUILD_TYPE = "https://slsa-framework.github.io/github-actions-buildtypes/workflow/v1";

function fail(message) {
  throw new Error(message);
}

function certificatePolicy(request) {
  return {
    "1.3.6.1.4.1.57264.1.1": ISSUER,
    "1.3.6.1.4.1.57264.1.3": request.commit,
    "1.3.6.1.4.1.57264.1.5": REPO,
    "1.3.6.1.4.1.57264.1.6": request.ref,
  };
}

function decode(statement) {
  const payload = statement.dsseEnvelope?.payload;
  if (!payload || statement.dsseEnvelope?.payloadType !== "application/vnd.in-toto+json") {
    fail("invalid DSSE envelope");
  }
  return JSON.parse(Buffer.from(payload, "base64").toString("utf8"));
}

async function main() {
  const requestPath = process.argv[2];
  if (!requestPath) {
    fail("usage: verify_sigstore_bundles.cjs REQUEST_JSON");
  }
  const request = JSON.parse(await fs.readFile(requestPath, "utf8"));
  if (!/^[0-9a-f]{40}$/.test(request.commit) || !/^refs\/tags\/v\d+\.\d+\.\d+$/.test(request.ref)) {
    fail("invalid release identity");
  }
  if (!Array.isArray(request.packages) || request.packages.length !== 5) {
    fail("expected five package bundles");
  }

  const sigstore = npmModule("sigstore");
  const npa = npmModule("npm-package-arg");
  const identity = `^https://github\\.com/${REPO}/\\.github/workflows/${WORKFLOW}@${request.ref.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`;
  for (const item of request.packages) {
    const matching = item.attestations.filter((entry) => entry.predicateType === SLSA_V1);
    if (matching.length !== 1 || !matching[0].bundle) {
      fail(`missing one SLSA provenance attestation: ${item.name}`);
    }
    const bundle = matching[0].bundle;
    if (!Array.isArray(bundle.verificationMaterial?.tlogEntries) ||
        bundle.verificationMaterial.tlogEntries.length !== 1) {
      fail(`expected one Rekor entry: ${item.name}`);
    }

    await sigstore.verify(bundle, {
      certificateOIDs: certificatePolicy(request),
      certificateIdentityURI: identity,
    });

    const payload = decode(bundle);
    const expectedName = npa.toPurl(npa(`${item.name}@${item.version}`));
    const subject = payload.subject?.[0];
    if (payload.predicateType !== SLSA_V1 || payload._type !== "https://in-toto.io/Statement/v1" ||
        payload.subject?.length !== 1 || subject?.name !== expectedName ||
        subject?.digest?.sha512 !== item.sha512) {
      fail(`provenance subject mismatch: ${item.name}`);
    }
    const definition = payload.predicate?.buildDefinition;
    const dependency = definition?.resolvedDependencies?.find((value) =>
      value.uri === `git+https://github.com/${REPO}@${request.ref}`);
    if (definition?.buildType !== BUILD_TYPE ||
        definition?.externalParameters?.workflow?.repository !== `https://github.com/${REPO}` ||
        definition.externalParameters.workflow.path !== `.github/workflows/${WORKFLOW}` ||
        definition.externalParameters.workflow.ref !== request.ref ||
        dependency?.digest?.gitCommit !== request.commit) {
      fail(`provenance source mismatch: ${item.name}`);
    }
  }
  process.stdout.write(`Verified independent Fulcio/Rekor provenance for ${request.packages.length} npm packages\n`);
}

main().catch((error) => {
  // Request contents are non-secret, but low-level failures can originate from
  // registry or signing clients. Do not accidentally forward their diagnostics.
  process.stderr.write(`Sigstore verification failed${error && error.message ? `: ${error.message}` : ""}\n`);
  process.exit(1);
});

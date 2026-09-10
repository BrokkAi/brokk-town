#!/usr/bin/env python3
"""Non-publishing release checks, also used by the publishing job before uploads."""

import argparse
import base64
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request

import package_installers
import package_registry
import package_release as release
import smoke_installers

REPO = "BrokkAi/brokk-town"
GH_REPO = "github.com/" + REPO
WORKFLOW = "publish-packages.yml"
ENVIRONMENT = "packages-publish"
NPM_NAMES = [f"@brokkai/brokk-town-{system}-{arch}"
             for system in ("linux", "darwin") for arch in ("x64", "arm64")] + [package_installers.NPM_ROOT]


def command(*args):
    return subprocess.check_output(args, text=True).strip()


def api(path, method="GET", body=None, missing=False):
    args = ["gh", "api", "--hostname", "github.com", f"repos/{REPO}/{path}", "--method", method]
    if body is not None:
        args += ["--input", "-"]
    result = subprocess.run(args, input=json.dumps(body) if body is not None else None,
                            text=True, capture_output=True, timeout=120)
    if result.returncode:
        if missing and "(HTTP 404)" in result.stderr:
            return None
        raise RuntimeError(f"GitHub {method} {path} failed: {result.stderr.strip()}")
    return json.loads(result.stdout) if result.stdout.strip() else None


def context():
    sha, tag = os.environ["RELEASE_COMMIT"], os.environ["RELEASE_TAG"]
    release.validate_tag(tag)
    if sha != release.commit() or release.run("git", "status", "--porcelain").strip():
        raise ValueError("release checks require the exact clean committed checkout")
    target = os.environ.get("RELEASE_TARGET", sha)
    subprocess.run(["git", "merge-base", "--is-ancestor", target, sha], check=True)
    return sha, tag


def tag_commit(tag):
    ref = api("git/ref/tags/" + urllib.parse.quote(tag, safe=""), missing=True)
    if ref is None:
        return None
    obj = ref["object"]
    for _ in range(5):
        if obj["type"] == "commit":
            return obj["sha"]
        if obj["type"] != "tag":
            break
        obj = api("git/tags/" + obj["sha"])["object"]
    raise ValueError("tag does not resolve to a commit")


def build(directory, sha, tag):
    if directory.exists():
        raise ValueError("build output must not exist; use a fresh directory")
    release.package(tag, directory / "native")
    package_installers.package(tag, directory / "native", directory / "packages", sha)
    smoke_installers.smoke(directory / "packages")
    (directory / "build-evidence.json").write_text(json.dumps({
        "commit": sha, "tag": tag, "targets": list(release.TARGETS), "packages": NPM_NAMES,
        "go": command("go", "version"), "node": command("node", "--version"),
        "npm": command("npm", "--version"),
    }, indent=2) + "\n")


def validate_build(directory, sha, tag):
    evidence = json.loads((directory / "build-evidence.json").read_text())
    if (evidence.get("commit") != sha or evidence.get("tag") != tag
            or evidence.get("targets") != list(release.TARGETS) or evidence.get("packages") != NPM_NAMES):
        raise ValueError("missing or mismatched build evidence")
    release.verify_local(tag, directory / "native", sha)
    packages = package_registry.validated_packages(directory / "packages")
    if any(p["version"] != tag[1:] for p in packages):
        raise ValueError("wrong npm release version")


def github_version(directory, sha, tag, published=False):
    remote_sha = tag_commit(tag)
    if remote_sha not in (None, sha) or (published and remote_sha != sha):
        raise ValueError("release tag missing or points to another commit")
    record = api("releases/tags/" + tag, missing=True)
    if record is None:
        if published:
            raise ValueError("GitHub release missing")
        return None
    if record["draft"] and record["target_commitish"] != sha:
        raise ValueError("existing draft must target the exact commit")
    if published and (record["draft"] or not record.get("published_at")):
        raise ValueError("GitHub release is not finalized")
    expected = {p.name for p in (directory / "native").iterdir()}
    assets = api(f"releases/{record['id']}/assets?per_page=100")
    names = [a["name"] for a in assets]
    if len(names) != len(set(names)) or not set(names) <= expected:
        raise ValueError("unexpected or duplicate GitHub assets")
    if (published or not record["draft"]) and set(names) != expected:
        raise ValueError("incomplete public GitHub release")
    if names:
        with tempfile.TemporaryDirectory() as temp:
            actual = Path(temp)
            subprocess.run(["gh", "release", "download", tag, "--repo", GH_REPO, "--dir", temp], check=True)
            if set(names) == expected:
                release.compare_assets(tag, actual, directory / "native", sha)
            else:
                # Partial drafts have no complete manifest. Only reuse exact staged bytes.
                for name in names:
                    if (actual / name).read_bytes() != (directory / "native" / name).read_bytes():
                        raise ValueError(f"conflicting partial draft asset: {name}")
    return record


def versions(directory, sha, tag):
    validate_build(directory, sha, tag)
    github_version(directory, sha, tag)
    package_registry.run("check", directory / "packages")


def request_json(url, token, method="GET"):
    request = urllib.request.Request(url, headers={"Authorization": "Bearer " + token,
                                                  "Accept": "application/json"}, method=method)
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        # Never log request headers, JWTs, exchange responses, or an Actions URL.
        raise RuntimeError(f"publisher identity check returned HTTP {error.code}") from None


def validate_claims(claims, sha, audience):
    now = datetime.now(timezone.utc).timestamp()
    expected = {"repository": REPO, "sha": sha, "environment": ENVIRONMENT,
                "aud": audience, "iss": "https://token.actions.githubusercontent.com"}
    if any(claims.get(k) != v for k, v in expected.items()):
        raise ValueError("OIDC identity does not match publishing context")
    if claims.get("exp", 0) <= now + 30 or claims.get("nbf", now + 1) > now:
        raise ValueError("OIDC identity expired or not yet valid")
    if not claims.get("workflow_ref", "").startswith(f"{REPO}/.github/workflows/{WORKFLOW}@"):
        raise ValueError("wrong npm trusted-publisher workflow")


def oidc_identity(sha, audience):
    url = os.environ["ACTIONS_ID_TOKEN_REQUEST_URL"]
    separator = "&" if "?" in url else "?"
    identity = request_json(url + separator + urllib.parse.urlencode({"audience": audience}),
                            os.environ["ACTIONS_ID_TOKEN_REQUEST_TOKEN"])["value"]
    payload = identity.split(".")[1]
    claims = json.loads(base64.urlsafe_b64decode(payload + "=" * (-len(payload) % 4)))
    validate_claims(claims, sha, audience)
    # The registry validates the signed identity; decoding claims alone is not proof.
    return identity


def validate_trust(configs):
    expected = {"repository": REPO, "workflow_ref": {"file": WORKFLOW}, "environment": ENVIRONMENT}
    if not isinstance(configs, list) or not any(
        c.get("type") == "github" and c.get("claims") == expected
        and "createPackage" in c.get("permissions", []) for c in configs
    ):
        raise ValueError("npm trust lacks exact repository/workflow/environment and direct publish permission")


def npm_authorization(sha):
    identity = oidc_identity(sha, "npm:registry.npmjs.org")
    for name in NPM_NAMES:
        encoded = urllib.parse.quote(name, safe="")
        exchange = request_json("https://registry.npmjs.org/-/npm/v1/oidc/token/exchange/package/" + encoded,
                                identity, "POST")
        expires = datetime.fromisoformat(exchange["expires"].replace("Z", "+00:00"))
        if exchange.get("token_type") != "oidc" or not exchange.get("token") or expires <= datetime.now(timezone.utc):
            raise ValueError("npm did not issue a valid unexpired package-scoped OIDC token")
        # Exchange can also grant staging-only rights. Require explicit direct-publish trust.
        # If npm denies this read to exchanged tokens, fail closed; do not use an upload as a probe.
        validate_trust(request_json("https://registry.npmjs.org/-/package/" + encoded + "/trust", exchange["token"]))
        print(f"Verified package OIDC exchange, expiry and direct publish trust: {name}")


def github_authorization(sha):
    if (os.environ.get("GITHUB_ACTIONS") != "true" or os.environ.get("GITHUB_REPOSITORY") != REPO
            or os.environ.get("GITHUB_JOB") != "packages" or os.environ.get("GITHUB_SHA") != sha):
        raise ValueError("authorization must execute in the actual Actions publishing job")
    probe = f"preflight-{os.environ['GITHUB_RUN_ID']}-{os.environ['GITHUB_RUN_ATTEMPT']}"
    if tag_commit(probe) is not None or api("releases/tags/" + probe, missing=True) is not None:
        raise ValueError("unexpected prior authorization probe; inspect before retrying")
    record = api("releases", "POST", {"tag_name": probe, "target_commitish": sha,
                                      "name": "Disposable release authorization probe", "draft": True})
    if not record.get("draft") or record.get("tag_name") != probe:
        raise ValueError("authorization probe did not return the requested private draft")
    api(f"releases/{record['id']}", "DELETE")
    if tag_commit(probe) is not None:
        raise ValueError("unexpected tag after private draft probe")
    print("Actions publisher created and deleted an asset-free private draft; contents write validated")


def authorization(sha):
    github_authorization(sha)
    npm_authorization(sha)
    # npm auto-provenance also contacts Sigstore. Do not mislabel obtaining an
    # identity token as proof that Fulcio/Rekor will accept the signing operation.
    raise RuntimeError("Sigstore provenance authorization is not yet checkable non-destructively; "
                       "provide a supported Fulcio/Rekor preflight before enabling publication")


def successful_run(run, sha, jobs, event):
    if run.get("head_sha") != sha or run.get("event") != event or run.get("status") != "completed":
        raise ValueError("wrong-commit, wrong-event or incomplete workflow run")
    if run.get("conclusion") != "success":
        raise ValueError("workflow did not succeed")
    if not jobs or any(j.get("status") != "completed" or j.get("conclusion") != "success" for j in jobs):
        raise ValueError("missing or unsuccessful workflow jobs")


def remote_evidence(sha, tag, gate, run_id):
    run = api(f"actions/runs/{run_id}")
    attempt = run["run_attempt"]
    jobs = api(f"actions/runs/{run_id}/attempts/{attempt}/jobs?per_page=100")["jobs"]
    if run.get("path") != ".github/workflows/" + WORKFLOW:
        raise ValueError("wrong release workflow")
    if run.get("display_title") != f"Release {tag} (publish=false)":
        raise ValueError("not a non-publishing preflight for the requested version")
    successful_run(run, sha, jobs, "workflow_dispatch")
    required = {"native / checks / test (ubuntu-latest)", "native / checks / test (macos-latest)",
                "native / checks / workflows", "native / build", "packages"}
    if {j["name"] for j in jobs} != required:
        raise ValueError("missing expected release jobs")
    artifact_name = f"release-candidate-{sha}-{tag}"
    artifacts = api(f"actions/runs/{run_id}/artifacts?per_page=100")["artifacts"]
    if not any(a["name"] == artifact_name and not a["expired"] and a["size_in_bytes"] > 0 for a in artifacts):
        raise ValueError("missing exact-version candidate artifact")
    with tempfile.TemporaryDirectory() as temp:
        subprocess.run(["gh", "run", "download", str(run_id), "--repo", GH_REPO,
                        "--name", artifact_name, "--dir", temp], check=True)
        validate_build(Path(temp), sha, tag)
        if gate == "version":
            versions(Path(temp), sha, tag)
    if gate == "authorization":
        # Cached runs cannot establish today's mutable environment policy/trust.
        # Require fresh publisher execution; never accept only a secret name/local login.
        completed = datetime.fromisoformat(run["updated_at"].replace("Z", "+00:00"))
        if (datetime.now(timezone.utc) - completed).total_seconds() > 900:
            raise ValueError("publisher authorization evidence expired; dispatch a new publish=false preflight")
    print(f"Verified {gate} from exact-SHA preflight run {run_id}, attempt {attempt}")


def published(directory, sha, tag):
    validate_build(directory, sha, tag)
    github_version(directory, sha, tag, published=True)
    package_registry.run("verify", directory / "packages")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("check", choices=("build", "version", "authorization", "published", "remote"))
    parser.add_argument("--directory", type=Path, default=Path("dist/candidate"))
    parser.add_argument("--gate", choices=("build", "version", "authorization"))
    parser.add_argument("--run-id", type=int)
    args = parser.parse_args()
    sha, tag = context()
    if args.check == "remote":
        if not args.gate or not args.run_id:
            parser.error("remote requires --gate and --run-id")
        remote_evidence(sha, tag, args.gate, args.run_id)
    elif args.check == "authorization":
        authorization(sha)
    else:
        {"build": build, "version": versions, "published": published}[args.check](args.directory, sha, tag)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, RuntimeError, KeyError, subprocess.CalledProcessError) as error:
        sys.exit(f"Release check failed: {error}")

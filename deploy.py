#!/usr/bin/env python3
import argparse
import os
import re
import subprocess
import urllib.request
import sys
import shutil
import json

def expand_env_vars(val):
    if isinstance(val, str):
        # Match ${VAR} or $VAR
        def replace(match):
            var_name = match.group(1) or match.group(2)
            # Standard bash-like expansion: empty string if environment variable is not defined
            return os.environ.get(var_name, "")
        pattern = re.compile(r'\$\{([^}]+)\}|\$(\w+)')
        return pattern.sub(replace, val)
    elif isinstance(val, list):
        return [expand_env_vars(item) for item in val]
    elif isinstance(val, dict):
        return {k: expand_env_vars(v) for k, v in val.items()}
    return val

def run_cmd(cmd, check=True, capture_output=False, dry_run=False):
    if dry_run:
        print(f"[DRY-RUN] Would run: {' '.join(cmd)}")
        class MockCompletedProcess:
            stdout = ""
            stderr = ""
        return MockCompletedProcess()

    print(f"Running: {' '.join(cmd)}")
    try:
        res = subprocess.run(
            cmd,
            check=check,
            stdout=subprocess.PIPE if capture_output else None,
            stderr=subprocess.PIPE if capture_output else None,
            text=True
        )
        return res
    except subprocess.CalledProcessError as e:
        print(f"Error executing command: {e}", file=sys.stderr)
        if e.stdout:
            print(f"Stdout:\n{e.stdout}", file=sys.stderr)
        if e.stderr:
            print(f"Stderr:\n{e.stderr}", file=sys.stderr)
        raise e

def generate_manifests(config, dry_run=False):
    # Add Helm Repositories
    repos = config.get("helm_repositories", [])
    if repos:
        print("Adding Helm repositories...")
        for repo in repos:
            name = repo["name"]
            url = repo["url"]
            run_cmd(["helm", "repo", "add", name, url], dry_run=dry_run)
        run_cmd(["helm", "repo", "update"], dry_run=dry_run)

    # Generate Helm Templates
    templates = config.get("helm_templates", [])
    for template in templates:
        name = template["name"]
        chart = template["chart"]
        namespace = template.get("namespace")
        version = template.get("version")
        output = template["output"]
        values = template.get("values", {})
        is_oci = template.get("is_oci", False) or chart.startswith("oci://")

        # Create output directory
        out_dir = os.path.dirname(output)
        if out_dir and not dry_run:
            os.makedirs(out_dir, exist_ok=True)

        if is_oci:
            # For OCI charts, pull and untar
            chart_dir = chart.split("/")[-1]
            pull_cmd = ["helm", "pull", chart, "--untar"]
            if version:
                pull_cmd.extend(["--version", version])
            print(f"Pulling OCI chart: {chart}")
            run_cmd(pull_cmd, dry_run=dry_run)
            template_chart = f"./{chart_dir}"
        else:
            template_chart = chart

        # Build template command
        cmd = ["helm", "template", name, template_chart]
        if namespace:
            cmd.extend(["--namespace", namespace])
        
        # OCI chart template doesn't need --version since we already pulled the specific version
        if version and not is_oci:
            cmd.extend(["--version", version])

        for k, v in values.items():
            if isinstance(v, bool):
                val_str = "true" if v else "false"
            else:
                val_str = str(v)
            cmd.extend(["--set", f"{k}={val_str}"])

        print(f"Generating template for {name} -> {output}")
        res = run_cmd(cmd, capture_output=True, dry_run=dry_run)
        if not dry_run:
            with open(output, "w") as f:
                f.write(res.stdout)

        # Clean up untarred directory if it was an OCI chart
        if is_oci and os.path.exists(template_chart):
            print(f"Cleaning up OCI chart directory: {template_chart}")
            if not dry_run:
                shutil.rmtree(template_chart)

    # Generate manifests
    manifests = config.get("manifests", [])
    for manifest in manifests:
        name = manifest["name"]
        output = manifest["output"]
        replacements = manifest.get("replacements", {})
        content = ""

        # Create output directory
        out_dir = os.path.dirname(output)
        if out_dir and not dry_run:
            os.makedirs(out_dir, exist_ok=True)

        if "url" in manifest:
            url = expand_env_vars(manifest["url"])
            print(f"Downloading manifest for {name} from {url}")
            if not dry_run:
                with urllib.request.urlopen(url) as response:
                    content = response.read().decode("utf-8")
            else:
                content = f"# Mocked content from {url}\n"
        elif "content" in manifest:
            content = expand_env_vars(manifest["content"])
        
        # Apply replacements
        for search_str, replace_str in replacements.items():
            search_str = expand_env_vars(search_str)
            replace_str = expand_env_vars(replace_str)
            content = content.replace(search_str, replace_str)

        print(f"Writing manifest for {name} -> {output}")
        if not dry_run:
            with open(output, "w") as f:
                f.write(content)

def deploy_manifests(config, dry_run=False):
    steps = config.get("deploy_steps", [])
    for step in steps:
        action = step["action"]
        if action == "apply":
            files = step.get("files", [])
            server_side = step.get("server_side", False)
            if not files:
                continue
            
            cmd = ["kubectl", "apply"]
            if server_side:
                cmd.append("--server-side")
            for file in files:
                cmd.extend(["-f", file])
            
            print(f"Applying: {', '.join(files)}")
            run_cmd(cmd, dry_run=dry_run)
        
        elif action == "patch":
            resource = step["resource"]
            namespace = step.get("namespace")
            patch_data = step["patch_data"]
            allow_failure = step.get("allow_failure", True)

            cmd = ["kubectl", "patch", resource]
            if namespace:
                cmd.extend(["-n", namespace])
            cmd.extend(["-p", patch_data])

            print(f"Patching resource {resource} in {namespace or 'default'}")
            try:
                run_cmd(cmd, dry_run=dry_run)
            except Exception as e:
                if not allow_failure:
                    raise e
                print(f"Ignored patch failure: {e}")

def main():
    parser = argparse.ArgumentParser(description="Kubernetes Deployment Script")
    parser.add_argument("--config", default="deploy-config.yml", help="Path to config file")
    parser.add_argument("--dry-run", action="store_true", help="Print commands instead of executing them")
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--generate", action="store_true", help="Generate manifests")
    group.add_argument("--apply", action="store_true", help="Apply manifests to Kubernetes")
    
    args = parser.parse_args()

    if not os.path.exists(args.config):
        print(f"Error: Config file '{args.config}' not found.", file=sys.stderr)
        sys.exit(1)

    if args.config.endswith(".json"):
        with open(args.config, "r") as f:
            config = json.load(f)
    else:
        try:
            import yaml
            with open(args.config, "r") as f:
                config = yaml.safe_load(f)
        except ImportError:
            print("Error: PyYAML is not installed. To parse YAML directly, install pyyaml, or pass a JSON file.", file=sys.stderr)
            sys.exit(1)

    # Recursively expand env vars in the loaded configuration
    config = expand_env_vars(config)

    if args.generate:
        generate_manifests(config, dry_run=args.dry_run)
    elif args.apply:
        deploy_manifests(config, dry_run=args.dry_run)

if __name__ == "__main__":
    main()

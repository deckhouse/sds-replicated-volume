#!/usr/bin/env python3

# Copyright 2025 Flant JSC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
# 
#     http://www.apache.org/licenses/LICENSE-2.0
# 
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# run from repository root

"""
Script for generating RELEASE_NOTES.md and RELEASE_NOTES.ru.md files
from files in the CHANGELOG folder.

The script parses YAML files with changes and creates markdown files
with release notes in the required format.
"""

import os
import yaml
import glob
import re
from pathlib import Path
from typing import List, Dict, Tuple
from packaging import version


def parse_version_from_filename(filename: str) -> str:
    """Extracts version from filename."""
    # Remove .yml and .ru extensions
    base_name = filename.replace('.ru.yml', '').replace('.yml', '')
    return base_name


def sort_files_by_version(files: List[str]) -> List[str]:
    """Sorts files by semantic version."""
    def version_key(filepath: str) -> version.Version:
        filename = os.path.basename(filepath)
        version_str = parse_version_from_filename(filename)
        try:
            # Handle versions that start with 'v' (e.g., v0.1.0)
            if version_str.startswith('v'):
                version_str = version_str[1:]
            return version.parse(version_str)
        except version.InvalidVersion:
            # Fallback to string comparison for invalid versions
            return version.parse("0.0.0")
    
    return sorted(files, key=version_key)


def load_changelog_file(filepath: str) -> Dict:
    """Loads and parses YAML file with changes."""
    try:
        with open(filepath, 'r', encoding='utf-8') as f:
            content = f.read()
            # Parse YAML with 2-space indentation
            return yaml.safe_load(content)
    except Exception as e:
        print(f"Error loading file {filepath}: {e}")
        return {}


def get_changelog_files(changelog_dir: str) -> Tuple[List[str], List[str]]:
    """Gets file lists for English and Russian versions."""
    en_files = glob.glob(os.path.join(changelog_dir, "*.yml"))
    ru_files = glob.glob(os.path.join(changelog_dir, "*.ru.yml"))
    
    # Filter files, excluding .ru.yml from English list
    en_files = [f for f in en_files if not f.endswith('.ru.yml')]
    
    # Sort files by semantic version
    en_files = sort_files_by_version(en_files)
    ru_files = sort_files_by_version(ru_files)
    
    return en_files, ru_files


def generate_markdown_content(files: List[str], changelog_dir: str, is_russian: bool = False) -> str:
    """Generates markdown file content."""
    content = []
    
    # Header
    if is_russian:
        content.append("---")
        content.append('title: "Релизы"')
        content.append("---")
        content.append("")
    else:
        content.append("---")
        content.append('title: "Release Notes"')
        content.append("---")
        content.append("")
    
    # Process files in reverse order (newest versions first)
    for filepath in reversed(files):
        version = parse_version_from_filename(os.path.basename(filepath))
        changelog_data = load_changelog_file(filepath)
        
        if not changelog_data:
            continue
            
        # Add version header
        content.append(f"## {version}")
        content.append("")
        
        # Add changes
        changes_key = "Изменения" if is_russian else "Changes"
        if changes_key in changelog_data:
            changes = changelog_data[changes_key]
            if isinstance(changes, list):
                for change in changes:
                    content.append(f"* {change}")
            else:
                content.append(f"* {changes}")
        content.append("")
    
    return "\n".join(content)


def remove_existing_files(output_dir: str):
    """Removes existing release notes files."""
    files_to_remove = [
        os.path.join(output_dir, "RELEASE_NOTES.md"),
        os.path.join(output_dir, "RELEASE_NOTES.ru.md")
    ]
    
    for filepath in files_to_remove:
        if os.path.exists(filepath):
            try:
                os.remove(filepath)
                print(f"Removed existing file: {filepath}")
            except Exception as e:
                print(f"Error removing file {filepath}: {e}")


def main():
    """Main script function."""
    # Define paths
    script_dir = Path(__file__).parent
    project_root = script_dir.parent
    changelog_dir = project_root / "CHANGELOG"
    output_dir = project_root / "docs"
    
    print(f"Working directory: {project_root}")
    print(f"Changelog folder: {changelog_dir}")
    print(f"Output folder: {output_dir}")
    
    # Check if CHANGELOG folder exists
    if not changelog_dir.exists():
        print(f"Error: folder {changelog_dir} not found")
        return 1
    
    # Get file lists
    en_files, ru_files = get_changelog_files(str(changelog_dir))
    
    if not en_files and not ru_files:
        print("No changelog files found")
        return 1
    
    print(f"Found {len(en_files)} English files and {len(ru_files)} Russian files")
    
    # Check that number of .ru.yml and .yml files match
    if len(en_files) != len(ru_files):
        print(f"Error: Number of English files ({len(en_files)}) does not match number of Russian files ({len(ru_files)})")
        return 1
    
    # Remove existing files
    remove_existing_files(str(output_dir))
    
    # Generate English version
    if en_files:
        en_content = generate_markdown_content(en_files, str(changelog_dir), is_russian=False)
        en_output_path = output_dir / "RELEASE_NOTES.md"
        
        try:
            with open(en_output_path, 'w', encoding='utf-8') as f:
                f.write(en_content)
            print(f"Created file: {en_output_path}")
        except Exception as e:
            print(f"Error creating file {en_output_path}: {e}")
            return 1
    
    # Generate Russian version
    if ru_files:
        ru_content = generate_markdown_content(ru_files, str(changelog_dir), is_russian=True)
        ru_output_path = output_dir / "RELEASE_NOTES.ru.md"
        
        try:
            with open(ru_output_path, 'w', encoding='utf-8') as f:
                f.write(ru_content)
            print(f"Created file: {ru_output_path}")
        except Exception as e:
            print(f"Error creating file {ru_output_path}: {e}")
            return 1
    
    print("Release notes generation completed successfully!")
    return 0


if __name__ == "__main__":
    exit(main())

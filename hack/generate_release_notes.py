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
Скрипт для генерации файлов RELEASE_NOTES.md и RELEASE_NOTES.ru.md
из файлов в папке CHANGELOG.

Скрипт парсит YAML файлы с изменениями и создает markdown файлы
с релизными заметками в требуемом формате.
"""

import os
import yaml
import glob
import re
from pathlib import Path
from typing import List, Dict, Tuple


def parse_version_from_filename(filename: str) -> str:
    """Извлекает версию из имени файла."""
    # Убираем расширение .yml и .ru
    base_name = filename.replace('.ru.yml', '').replace('.yml', '')
    return base_name


def load_changelog_file(filepath: str) -> Dict:
    """Загружает и парсит YAML файл с изменениями."""
    try:
        with open(filepath, 'r', encoding='utf-8') as f:
            content = f.read()
            # Парсим YAML с учетом отступов в 2 пробела
            return yaml.safe_load(content)
    except Exception as e:
        print(f"Ошибка при загрузке файла {filepath}: {e}")
        return {}


def get_changelog_files(changelog_dir: str) -> Tuple[List[str], List[str]]:
    """Получает списки файлов для английской и русской версий."""
    en_files = glob.glob(os.path.join(changelog_dir, "*.yml"))
    ru_files = glob.glob(os.path.join(changelog_dir, "*.ru.yml"))
    
    # Фильтруем файлы, исключая .ru.yml из английского списка
    en_files = [f for f in en_files if not f.endswith('.ru.yml')]
    
    return sorted(en_files), sorted(ru_files)


def generate_markdown_content(files: List[str], changelog_dir: str, is_russian: bool = False) -> str:
    """Генерирует содержимое markdown файла."""
    content = []
    
    # Заголовок
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
    
    # Обрабатываем файлы в обратном порядке (новые версии первыми)
    for filepath in reversed(files):
        version = parse_version_from_filename(os.path.basename(filepath))
        changelog_data = load_changelog_file(filepath)
        
        if not changelog_data:
            continue
            
        # Добавляем заголовок версии
        content.append(f"## {version}")
        content.append("")
        
        # Добавляем изменения
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
    """Удаляет существующие файлы release notes."""
    files_to_remove = [
        os.path.join(output_dir, "RELEASE_NOTES.md"),
        os.path.join(output_dir, "RELEASE_NOTES.ru.md")
    ]
    
    for filepath in files_to_remove:
        if os.path.exists(filepath):
            try:
                os.remove(filepath)
                print(f"Удален существующий файл: {filepath}")
            except Exception as e:
                print(f"Ошибка при удалении файла {filepath}: {e}")


def main():
    """Основная функция скрипта."""
    # Определяем пути
    script_dir = Path(__file__).parent
    project_root = script_dir.parent
    changelog_dir = project_root / "CHANGELOG"
    output_dir = project_root / "docs"
    
    print(f"Рабочая директория: {project_root}")
    print(f"Папка с changelog: {changelog_dir}")
    print(f"Папка для вывода: {output_dir}")
    
    # Проверяем существование папки CHANGELOG
    if not changelog_dir.exists():
        print(f"Ошибка: папка {changelog_dir} не найдена")
        return 1
    
    # Получаем списки файлов
    en_files, ru_files = get_changelog_files(str(changelog_dir))
    
    if not en_files and not ru_files:
        print("Не найдено файлов changelog")
        return 1
    
    print(f"Найдено {len(en_files)} английских файлов и {len(ru_files)} русских файлов")
    
    # Удаляем существующие файлы
    remove_existing_files(str(output_dir))
    
    # Генерируем английскую версию
    if en_files:
        en_content = generate_markdown_content(en_files, str(changelog_dir), is_russian=False)
        en_output_path = output_dir / "RELEASE_NOTES.md"
        
        try:
            with open(en_output_path, 'w', encoding='utf-8') as f:
                f.write(en_content)
            print(f"Создан файл: {en_output_path}")
        except Exception as e:
            print(f"Ошибка при создании файла {en_output_path}: {e}")
            return 1
    
    # Генерируем русскую версию
    if ru_files:
        ru_content = generate_markdown_content(ru_files, str(changelog_dir), is_russian=True)
        ru_output_path = output_dir / "RELEASE_NOTES.ru.md"
        
        try:
            with open(ru_output_path, 'w', encoding='utf-8') as f:
                f.write(ru_content)
            print(f"Создан файл: {ru_output_path}")
        except Exception as e:
            print(f"Ошибка при создании файла {ru_output_path}: {e}")
            return 1
    
    print("Генерация release notes завершена успешно!")
    return 0


if __name__ == "__main__":
    exit(main())

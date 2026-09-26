import fnmatch
import importlib.util
from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parent.parent

class ProjectReleaseTests(unittest.TestCase):
    def test_project_tags_route_to_exactly_one_workflow(self):
        patterns = {}
        for path in (ROOT / '.github/workflows').glob('release-*.yml'):
            match = re.search(r"tags: \['([^']+)'\]", path.read_text())
            self.assertIsNotNone(match, path)
            patterns[path.stem.removeprefix('release-')] = match.group(1)
        projects = {'town', *(p.name for p in (ROOT / 'bots').iterdir() if (p / 'go.mod').exists())}
        self.assertEqual(set(patterns), projects)
        for project in projects:
            for version in ('v1.2.3', 'v1.2.3-rc.1'):
                tag = version + '-' + project
                matches = [name for name, pattern in patterns.items() if fnmatch.fnmatchcase(tag, pattern)]
                self.assertEqual(matches, [project])
                directory = ROOT if project == 'town' else ROOT / 'bots' / project
                script = directory / 'scripts' / ('release.py' if project == 'release-bot' else 'package_release.py')
                spec = importlib.util.spec_from_file_location('fixture_release', script)
                module = importlib.util.module_from_spec(spec)
                spec.loader.exec_module(module)
                self.assertEqual(module.version_tag(tag), version)


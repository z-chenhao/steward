"""Channel wait predicates must observe one snapshot and release their reader."""
from contextlib import closing
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import patch

from verify_channels import ChannelHarness


class ChannelSnapshotTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        (self.root / 'data').mkdir()
        self.path = self.root / 'data/state.db'
        # Only the observer's projection is needed. Writers still use real WAL
        # transactions on a separate connection, without sleeps or subprocesses.
        with closing(sqlite3.connect(self.path)) as db, db:
            db.execute('PRAGMA journal_mode=WAL')
            for table, columns in {
                'main_input': 'request_key,message_id,run_id,sequence',
                'channel_callback': 'event_id',
                'delivery_request': 'delivery_id,message_id,channel,destination_ref,status,attempt_count,version,created_at',
                'channel_receipt': 'delivery_id,part',
                'approval': 'approval_id,frozen_arguments_hash,status,created_at',
                'tool_invocation': 'tool_name,arguments_json,status,attempt_count,created_at,error_json',
                'run': 'run_id,kind,worker_position,status,created_at',
                'channel_resource': 'id',
                'delivery_projection': 'id',
            }.items():
                db.execute(f'CREATE TABLE {table} ({columns})')
            db.execute("INSERT INTO run VALUES ('main','main','','running',1)")
            db.execute("INSERT INTO delivery_request VALUES ('old','old','web','','sent',1,1,1)")
        self.harness = object.__new__(ChannelHarness)
        self.harness.root = self.root

    def test_reply_enqueued_between_reads_cannot_look_settled(self):
        connect, path = sqlite3.connect, self.path

        class InterleavedReader(sqlite3.Connection):
            def execute(self, sql, *args, **kwargs):
                if sql.startswith('SELECT run_id,kind,worker_position,status FROM run'):
                    with closing(connect(path)) as writer, writer:
                        writer.execute("INSERT INTO delivery_request VALUES ('new','new','web','','pending',0,1,2)")
                        writer.execute("UPDATE run SET status='completed'")
                return super().execute(sql, *args, **kwargs)

        with patch('verify_channels.sqlite3.connect',
                   side_effect=lambda *a, **kw: connect(*a, **kw, factory=InterleavedReader)):
            state = self.harness.rows()
        self.assertEqual(state['runs'][0]['status'], 'running')
        self.assertEqual([d['status'] for d in state['deliveries']], ['sent'])
        next_state = self.harness.rows()
        self.assertEqual(next_state['runs'][0]['status'], 'completed')
        self.assertEqual([d['status'] for d in next_state['deliveries']], ['sent', 'pending'])

    def test_closes_reader_on_success_and_query_failure(self):
        for fail in (False, True):
            with self.subTest(fail=fail):
                reader = sqlite3.connect(self.path)
                if fail:
                    reader.execute('DROP TABLE delivery_request')
                with patch('verify_channels.sqlite3.connect', return_value=reader):
                    if fail:
                        with self.assertRaises(sqlite3.OperationalError):
                            self.harness.rows()
                    else:
                        self.harness.rows()
                try:
                    with self.assertRaises(sqlite3.ProgrammingError):
                        reader.execute('SELECT 1')
                finally:
                    reader.close()

    def test_plugin_bootstrap_uses_control_flags_and_restores_runtime_fixture(self):
        for fails in (False,True):
            with self.subTest(fails=fails):
                self.harness.fixture='terminal-input'
                self.harness.production_events=True
                def command(*args):
                    self.assertEqual(args,('channel','status'))
                    self.assertEqual(self.harness.fixture,'')
                    self.assertFalse(self.harness.production_events)
                    if fails:
                        raise RuntimeError('controlled bootstrap failure')
                self.harness.command=command
                if fails:
                    with self.assertRaises(RuntimeError):
                        self.harness.enable_default_plugins()
                else:
                    self.harness.enable_default_plugins()
                self.assertEqual(self.harness.fixture,'terminal-input')
                self.assertTrue(self.harness.production_events)

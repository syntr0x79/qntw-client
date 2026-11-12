"""Keep unrelated GTK fixtures away from saved sessions and local credentials."""
from unittest.mock import DEFAULT, patch


def block_auth_io(target):
    """Call before activate; dedicated persistence tests inject their own stores."""
    guard = patch.multiple(target, start_session_restore=DEFAULT,
                           persist_account_session=DEFAULT, forget_saved_account=DEFAULT)
    guard.start()
    return guard

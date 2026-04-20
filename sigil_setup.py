"""Sigil SDK setup: lazy singleton Grafana Sigil client + LangChain callback helper.

Enable by setting the following environment variables:

    GRAFANA_CLOUD_SIGIL_ENDPOINT  e.g. https://<stack>.grafana.net/api/v1/generations:export
    GRAFANA_CLOUD_INSTANCE_ID     your Grafana Cloud instance ID (same as OTLP username)
                                  (also accepted: GRAFANA_CLOUD_INSTANCE)
    GRAFANA_CLOUD_API_KEY         a Grafana Cloud API key with Sigil write scope

If ``GRAFANA_CLOUD_SIGIL_ENDPOINT`` is unset, the module operates in a no-op
mode so local development and tests do not require Sigil credentials.

The agent_version sent to Sigil is resolved in this order:
    1. env var ``ASIMOV_AGENT_VERSION`` if set
    2. short git SHA from ``.git/HEAD`` if available (dev convenience)
    3. fallback ``"1.0.0"``
"""

from __future__ import annotations

import atexit
import logging
import os
from pathlib import Path
from typing import Any, Optional

logger = logging.getLogger(__name__)

_client: Optional[Any] = None
_init_failed: bool = False
_agent_version_cache: Optional[str] = None


AGENT_NAME = "asimov-dnd"
_DEFAULT_AGENT_VERSION = "1.0.0"
_PROVIDER = "anthropic"
_COMPONENT_TAG = "sigil.component"


def _resolve_git_short_sha(repo_root: Path) -> Optional[str]:
    """Read ``.git/HEAD`` and resolve the current commit to a 7-char SHA.

    Returns None if the repo is not a git checkout, is in an unusual state,
    or the SHA cannot be read for any reason. Intentionally does NOT shell
    out to ``git``: pure filesystem reads only.
    """
    try:
        head_file = repo_root / ".git" / "HEAD"
        if not head_file.exists():
            return None
        head_content = head_file.read_text(encoding="utf-8").strip()
        if head_content.startswith("ref: "):
            ref_path = repo_root / ".git" / head_content[len("ref: "):].strip()
            if ref_path.exists():
                sha = ref_path.read_text(encoding="utf-8").strip()
                return sha[:7] if sha else None
            packed_refs = repo_root / ".git" / "packed-refs"
            if packed_refs.exists():
                ref_name = head_content[len("ref: "):].strip()
                for line in packed_refs.read_text(encoding="utf-8").splitlines():
                    if line.startswith("#") or "^" in line:
                        continue
                    parts = line.strip().split(" ", 1)
                    if len(parts) == 2 and parts[1] == ref_name:
                        return parts[0][:7]
            return None
        return head_content[:7]
    except Exception:
        return None


def _resolve_agent_version() -> str:
    """Pick the agent_version per docstring precedence. Cached per process."""
    global _agent_version_cache
    if _agent_version_cache is not None:
        return _agent_version_cache

    env_ver = os.getenv("ASIMOV_AGENT_VERSION", "").strip()
    if env_ver:
        _agent_version_cache = env_ver
        return _agent_version_cache

    sha = _resolve_git_short_sha(Path(__file__).resolve().parent)
    if sha:
        _agent_version_cache = f"git-{sha}"
        return _agent_version_cache

    _agent_version_cache = _DEFAULT_AGENT_VERSION
    return _agent_version_cache


def _build_client() -> Optional[Any]:
    """Construct the Sigil client, or return None if disabled/unavailable."""
    endpoint = os.getenv("GRAFANA_CLOUD_SIGIL_ENDPOINT", "").strip()
    if not endpoint:
        return None

    try:
        from sigil_sdk import (  # type: ignore[import-not-found]
            AuthConfig,
            Client,
            ClientConfig,
            GenerationExportConfig,
        )
    except ImportError as exc:
        logger.warning("sigil-sdk not installed; Sigil instrumentation disabled: %s", exc)
        return None

    instance_id = (
        os.getenv("GRAFANA_CLOUD_INSTANCE_ID", "").strip()
        or os.getenv("GRAFANA_CLOUD_INSTANCE", "").strip()
    )
    api_key = os.getenv("GRAFANA_CLOUD_API_KEY", "").strip()
    if not instance_id or not api_key:
        logger.warning(
            "GRAFANA_CLOUD_SIGIL_ENDPOINT is set but GRAFANA_CLOUD_INSTANCE_ID/"
            "GRAFANA_CLOUD_INSTANCE or GRAFANA_CLOUD_API_KEY is missing; "
            "Sigil instrumentation disabled."
        )
        return None

    cfg = ClientConfig(
        generation_export=GenerationExportConfig(
            protocol="http",
            endpoint=endpoint,
            auth=AuthConfig(
                mode="basic",
                tenant_id=instance_id,
                basic_password=api_key,
            ),
        ),
    )
    client = Client(cfg)
    atexit.register(_safe_shutdown, client)
    logger.info(
        "Sigil client initialised (endpoint=%s, agent=%s@%s)",
        endpoint,
        AGENT_NAME,
        _resolve_agent_version(),
    )
    return client


def _safe_shutdown(client: Any) -> None:
    try:
        client.shutdown()
    except Exception as exc:
        logger.warning("Sigil client shutdown raised: %s", exc)


def get_sigil_client() -> Optional[Any]:
    """Return the process-wide Sigil client, initialising it on first use.

    Returns ``None`` when Sigil is disabled (missing env vars or sigil-sdk
    package not installed).
    """
    global _client, _init_failed
    if _client is not None:
        return _client
    if _init_failed:
        return None
    try:
        _client = _build_client()
    except Exception as exc:
        logger.warning("Failed to initialise Sigil client: %s", exc)
        _init_failed = True
        return None
    if _client is None:
        _init_failed = True
    return _client


def sigil_langchain_config(
    extra: Optional[dict] = None,
    *,
    component: Optional[str] = None,
    extra_metadata: Optional[dict] = None,
) -> dict:
    """Return a LangChain ``config=`` dict with Sigil callbacks attached.

    Parameters
    ----------
    extra : dict | None
        Existing LangChain config to merge into (e.g. ``{"tags": [...]}``).
    component : str | None
        Low-cardinality component label (e.g. ``"classifier"``, ``"dialogue"``).
        Attached as the ``sigil.component`` tag on recorded generations so
        Sigil's agent catalog can split per-component cost/latency rollups.
    extra_metadata : dict | None
        Arbitrary metadata dict forwarded to the generation recorder via the
        handler. Used for opportunity 2c (multi-agent DAG placeholder links
        under ``sigil.run.id`` / ``sigil.run.parent_ids``).

    When Sigil is disabled, returns ``extra`` unchanged (defensive copy).
    """
    client = get_sigil_client()
    if client is None:
        return dict(extra) if extra else {}

    try:
        from sigil_sdk_langchain import (  # type: ignore[import-not-found]
            with_sigil_langchain_callbacks,
        )
    except ImportError as exc:
        logger.warning(
            "sigil-sdk-langchain not installed; LangChain callbacks disabled: %s", exc
        )
        return dict(extra) if extra else {}

    handler_kwargs: dict[str, Any] = {
        "agent_name": AGENT_NAME,
        "agent_version": _resolve_agent_version(),
        "provider": _PROVIDER,
    }
    if component:
        handler_kwargs["extra_tags"] = {_COMPONENT_TAG: component}
    if extra_metadata:
        handler_kwargs["extra_metadata"] = dict(extra_metadata)

    try:
        return with_sigil_langchain_callbacks(extra, client=client, **handler_kwargs)
    except TypeError:
        handler_kwargs.pop("provider", None)
        handler_kwargs["provider_resolver"] = "auto"
        return with_sigil_langchain_callbacks(extra, client=client, **handler_kwargs)


def reset_for_tests() -> None:
    """Reset module-level state. Intended for tests only."""
    global _client, _init_failed, _agent_version_cache
    _client = None
    _init_failed = False
    _agent_version_cache = None

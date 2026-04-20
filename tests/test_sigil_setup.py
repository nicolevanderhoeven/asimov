"""Unit tests for sigil_setup module."""

from __future__ import annotations

import sys
import types
from unittest.mock import MagicMock, patch

import pytest


@pytest.fixture(autouse=True)
def _reset_sigil_state(monkeypatch):
    """Reset sigil_setup module state and env between tests."""
    for var in (
        "GRAFANA_CLOUD_SIGIL_ENDPOINT",
        "GRAFANA_CLOUD_INSTANCE_ID",
        "GRAFANA_CLOUD_INSTANCE",
        "GRAFANA_CLOUD_API_KEY",
        "ASIMOV_AGENT_VERSION",
    ):
        monkeypatch.delenv(var, raising=False)

    import sigil_setup
    sigil_setup.reset_for_tests()
    yield
    sigil_setup.reset_for_tests()


def _install_fake_sigil_sdk(monkeypatch):
    """Install stub sigil_sdk and sigil_sdk_langchain modules into sys.modules."""
    fake_sdk = types.ModuleType("sigil_sdk")

    class _AuthConfig:
        def __init__(self, **kwargs):
            self.__dict__.update(kwargs)

    class _GenerationExportConfig:
        def __init__(self, **kwargs):
            self.__dict__.update(kwargs)

    class _ClientConfig:
        def __init__(self, **kwargs):
            self.__dict__.update(kwargs)

    client_instance = MagicMock(name="SigilClient")
    client_factory = MagicMock(name="Client", return_value=client_instance)

    fake_sdk.AuthConfig = _AuthConfig
    fake_sdk.Client = client_factory
    fake_sdk.ClientConfig = _ClientConfig
    fake_sdk.GenerationExportConfig = _GenerationExportConfig

    fake_lc = types.ModuleType("sigil_sdk_langchain")

    def _with_callbacks(extra, *, client, **kwargs):
        merged = dict(extra) if extra else {}
        merged.setdefault("callbacks", [])
        merged["callbacks"] = list(merged["callbacks"]) + ["sigil-handler"]
        merged["_sigil_wired"] = True
        merged["_sigil_handler_kwargs"] = dict(kwargs)
        return merged

    fake_lc.with_sigil_langchain_callbacks = _with_callbacks

    monkeypatch.setitem(sys.modules, "sigil_sdk", fake_sdk)
    monkeypatch.setitem(sys.modules, "sigil_sdk_langchain", fake_lc)
    return fake_sdk, fake_lc, client_instance


class TestDisabledMode:
    """Without GRAFANA_CLOUD_SIGIL_ENDPOINT set, Sigil is a no-op."""

    def test_get_client_returns_none(self):
        import sigil_setup
        assert sigil_setup.get_sigil_client() is None

    def test_langchain_config_returns_empty_dict(self):
        import sigil_setup
        assert sigil_setup.sigil_langchain_config() == {}

    def test_langchain_config_passes_through_extra(self):
        import sigil_setup
        extra = {"tags": ["a"], "callbacks": ["existing"]}
        result = sigil_setup.sigil_langchain_config(extra)
        assert result == extra
        assert result is not extra


class TestEnabledMode:
    """With env vars set, Sigil client is constructed with correct auth."""

    def test_client_constructed_with_basic_auth(self, monkeypatch):
        fake_sdk, _, client_instance = _install_fake_sigil_sdk(monkeypatch)
        monkeypatch.setenv(
            "GRAFANA_CLOUD_SIGIL_ENDPOINT",
            "https://stack.grafana.net/api/v1/generations:export",
        )
        monkeypatch.setenv("GRAFANA_CLOUD_INSTANCE_ID", "12345")
        monkeypatch.setenv("GRAFANA_CLOUD_API_KEY", "glc_api_key_here")

        with patch("atexit.register") as atexit_register:
            import sigil_setup
            client = sigil_setup.get_sigil_client()

        assert client is client_instance
        assert fake_sdk.Client.call_count == 1
        cfg = fake_sdk.Client.call_args.args[0]
        auth = cfg.generation_export.auth
        assert auth.mode == "basic"
        assert auth.tenant_id == "12345"
        assert auth.basic_password == "glc_api_key_here"
        assert cfg.generation_export.endpoint == (
            "https://stack.grafana.net/api/v1/generations:export"
        )
        atexit_register.assert_called_once()

    def test_client_is_singleton(self, monkeypatch):
        fake_sdk, _, _ = _install_fake_sigil_sdk(monkeypatch)
        monkeypatch.setenv(
            "GRAFANA_CLOUD_SIGIL_ENDPOINT",
            "https://stack.grafana.net/api/v1/generations:export",
        )
        monkeypatch.setenv("GRAFANA_CLOUD_INSTANCE_ID", "12345")
        monkeypatch.setenv("GRAFANA_CLOUD_API_KEY", "glc_api_key_here")

        import sigil_setup
        c1 = sigil_setup.get_sigil_client()
        c2 = sigil_setup.get_sigil_client()
        assert c1 is c2
        assert fake_sdk.Client.call_count == 1

    def test_langchain_config_merges_callbacks(self, monkeypatch):
        _install_fake_sigil_sdk(monkeypatch)
        monkeypatch.setenv(
            "GRAFANA_CLOUD_SIGIL_ENDPOINT",
            "https://stack.grafana.net/api/v1/generations:export",
        )
        monkeypatch.setenv("GRAFANA_CLOUD_INSTANCE_ID", "12345")
        monkeypatch.setenv("GRAFANA_CLOUD_API_KEY", "glc_api_key_here")

        import sigil_setup
        result = sigil_setup.sigil_langchain_config({"tags": ["a"]})
        assert result["tags"] == ["a"]
        assert "sigil-handler" in result["callbacks"]
        assert result["_sigil_wired"] is True


class TestMisconfiguration:
    """Endpoint set but credentials missing -> disabled with warning."""

    def test_missing_instance_id_disables(self, monkeypatch):
        _install_fake_sigil_sdk(monkeypatch)
        monkeypatch.setenv(
            "GRAFANA_CLOUD_SIGIL_ENDPOINT",
            "https://stack.grafana.net/api/v1/generations:export",
        )
        monkeypatch.setenv("GRAFANA_CLOUD_API_KEY", "glc_api_key_here")

        import sigil_setup
        assert sigil_setup.get_sigil_client() is None

    def test_missing_api_key_disables(self, monkeypatch):
        _install_fake_sigil_sdk(monkeypatch)
        monkeypatch.setenv(
            "GRAFANA_CLOUD_SIGIL_ENDPOINT",
            "https://stack.grafana.net/api/v1/generations:export",
        )
        monkeypatch.setenv("GRAFANA_CLOUD_INSTANCE_ID", "12345")

        import sigil_setup
        assert sigil_setup.get_sigil_client() is None


class TestEnhancedHandlerConfig:
    """Opportunities 1, 2c, 3: component tag, extra_metadata, explicit provider."""

    def _enable(self, monkeypatch):
        _install_fake_sigil_sdk(monkeypatch)
        monkeypatch.setenv(
            "GRAFANA_CLOUD_SIGIL_ENDPOINT",
            "https://stack.grafana.net/api/v1/generations:export",
        )
        monkeypatch.setenv("GRAFANA_CLOUD_INSTANCE_ID", "12345")
        monkeypatch.setenv("GRAFANA_CLOUD_API_KEY", "glc_api_key_here")

    def test_provider_is_explicit_anthropic(self, monkeypatch):
        self._enable(monkeypatch)
        import sigil_setup
        result = sigil_setup.sigil_langchain_config()
        kwargs = result["_sigil_handler_kwargs"]
        assert kwargs["provider"] == "anthropic"
        assert "provider_resolver" not in kwargs

    def test_agent_name_and_version_forwarded(self, monkeypatch):
        self._enable(monkeypatch)
        monkeypatch.setenv("ASIMOV_AGENT_VERSION", "v9.9.9")
        import sigil_setup
        sigil_setup.reset_for_tests()
        result = sigil_setup.sigil_langchain_config()
        kwargs = result["_sigil_handler_kwargs"]
        assert kwargs["agent_name"] == "asimov-dnd"
        assert kwargs["agent_version"] == "v9.9.9"

    def test_component_becomes_sigil_component_tag(self, monkeypatch):
        self._enable(monkeypatch)
        import sigil_setup
        result = sigil_setup.sigil_langchain_config(component="classifier")
        kwargs = result["_sigil_handler_kwargs"]
        assert kwargs["extra_tags"] == {"sigil.component": "classifier"}

    def test_no_component_omits_extra_tags(self, monkeypatch):
        self._enable(monkeypatch)
        import sigil_setup
        result = sigil_setup.sigil_langchain_config()
        kwargs = result["_sigil_handler_kwargs"]
        assert "extra_tags" not in kwargs

    def test_extra_metadata_forwarded(self, monkeypatch):
        self._enable(monkeypatch)
        import sigil_setup
        result = sigil_setup.sigil_langchain_config(
            component="gm_qa",
            extra_metadata={"sigil.run.parent_ids": ["abc123"]},
        )
        kwargs = result["_sigil_handler_kwargs"]
        assert kwargs["extra_metadata"] == {"sigil.run.parent_ids": ["abc123"]}

    def test_extra_metadata_is_copied_not_aliased(self, monkeypatch):
        self._enable(monkeypatch)
        import sigil_setup
        meta = {"sigil.run.id": "abc"}
        result = sigil_setup.sigil_langchain_config(extra_metadata=meta)
        meta["sigil.run.id"] = "mutated"
        assert result["_sigil_handler_kwargs"]["extra_metadata"] == {"sigil.run.id": "abc"}


class TestAgentVersionResolver:
    """agent_version precedence: env var -> git SHA -> '1.0.0'."""

    def test_env_var_wins(self, monkeypatch):
        monkeypatch.setenv("ASIMOV_AGENT_VERSION", "v0.3.1")
        import sigil_setup
        sigil_setup.reset_for_tests()
        assert sigil_setup._resolve_agent_version() == "v0.3.1"

    def test_git_sha_when_no_env(self, monkeypatch):
        import sigil_setup
        sigil_setup.reset_for_tests()
        version = sigil_setup._resolve_agent_version()
        assert version.startswith("git-") or version == "1.0.0"
        if version.startswith("git-"):
            assert len(version) == len("git-") + 7

    def test_fallback_to_constant(self, monkeypatch, tmp_path):
        fake_head = tmp_path / ".git" / "HEAD"
        import sigil_setup
        sigil_setup.reset_for_tests()
        assert sigil_setup._resolve_git_short_sha(tmp_path) is None


class TestSdkNotInstalled:
    """When sigil_sdk import fails, module degrades to no-op."""

    def test_missing_sdk_returns_none(self, monkeypatch):
        monkeypatch.setenv(
            "GRAFANA_CLOUD_SIGIL_ENDPOINT",
            "https://stack.grafana.net/api/v1/generations:export",
        )
        monkeypatch.setenv("GRAFANA_CLOUD_INSTANCE_ID", "12345")
        monkeypatch.setenv("GRAFANA_CLOUD_API_KEY", "glc_api_key_here")

        real_import = __builtins__["__import__"] if isinstance(__builtins__, dict) else __import__

        def fake_import(name, *args, **kwargs):
            if name == "sigil_sdk":
                raise ImportError("sigil_sdk not available")
            return real_import(name, *args, **kwargs)

        with patch("builtins.__import__", side_effect=fake_import):
            import sigil_setup
            assert sigil_setup.get_sigil_client() is None
            assert sigil_setup.sigil_langchain_config({"k": 1}) == {"k": 1}

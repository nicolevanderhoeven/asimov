"""Tests for the Positronic Operative class and Data's character build."""
from character_classes import PositronicOperative, build_data_character


class TestPositronicOperative:
    def test_hit_die(self):
        cls = PositronicOperative()
        assert cls.hit_die == "d10"

    def test_proficiency_bonus(self):
        cls = PositronicOperative()
        assert cls.proficiency_bonus == 2

    def test_saving_throws(self):
        cls = PositronicOperative()
        assert "STR" in cls.saving_throw_proficiencies
        assert "CON" in cls.saving_throw_proficiencies

    def test_default_skills(self):
        cls = PositronicOperative()
        assert "athletics" in cls.default_skill_proficiencies
        assert "investigation" in cls.default_skill_proficiencies

    def test_ac_computation(self):
        cls = PositronicOperative()
        assert cls.compute_ac(dex_modifier=1) == 14  # 13 + 1 DEX

    def test_ac_max_dex_bonus_capped(self):
        cls = PositronicOperative()
        assert cls.compute_ac(dex_modifier=5) == 15  # 13 + 2 (capped at max_dex_bonus)


class TestBuildDataCharacter:
    def test_hp(self):
        data = build_data_character()
        assert data.hp == 12  # 10 + CON mod (14 → +2)

    def test_ac(self):
        data = build_data_character()
        assert data.armor_class == 14  # From starter_character

    def test_name(self):
        data = build_data_character()
        assert data.name == "Data"

    def test_class(self):
        data = build_data_character()
        assert data.character_class == "Positronic Operative"

    def test_proficiency_bonus(self):
        data = build_data_character()
        assert data.proficiency_bonus == 2

    def test_str_modifier(self):
        data = build_data_character()
        assert data.ability_modifier("STR") == 2

    def test_cha_modifier(self):
        data = build_data_character()
        assert data.ability_modifier("CHA") == -1

    def test_skill_proficiencies(self):
        data = build_data_character()
        assert "athletics" in data.skill_proficiencies
        assert "investigation" in data.skill_proficiencies

    def test_saving_throw_proficiencies(self):
        data = build_data_character()
        assert "STR" in data.saving_throw_proficiencies
        assert "CON" in data.saving_throw_proficiencies

    def test_equipment_count(self):
        data = build_data_character()
        assert len(data.equipment) == 3

    def test_class_features(self):
        data = build_data_character()
        assert "self_repair_cycle" in data.class_features
        assert "subroutine_focus" in data.class_features

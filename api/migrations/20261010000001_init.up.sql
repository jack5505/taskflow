-- Базовые расширения. citext нужен для email без учёта регистра (ТЗ §3.1: email уникален).
CREATE EXTENSION IF NOT EXISTS citext;

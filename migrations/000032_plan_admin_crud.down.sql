ALTER TABLE plan ADD CONSTRAINT plan_name_check CHECK (name IN ('free', 'pro', 'max')) NOT VALID;

-- A person can turn the console's one-character shortcuts off, and the choice follows the account
-- (UX-122, WCAG 2.2 criterion 2.1.4). Existing rows keep their revision: nobody chose this.
ALTER TABLE user_preferences ADD COLUMN single_key_shortcuts boolean NOT NULL DEFAULT true;

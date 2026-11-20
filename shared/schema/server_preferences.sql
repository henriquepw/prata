ALTER TABLE users ADD COLUMN language TEXT NOT NULL DEFAULT 'pt-BR' CHECK (language IN ('pt-BR', 'en'));
ALTER TABLE users ADD COLUMN date_format TEXT NOT NULL DEFAULT 'DD/MM/YYYY' CHECK (date_format IN ('DD/MM/YYYY', 'MM/DD/YYYY', 'YYYY-MM-DD'));
ALTER TABLE users ADD COLUMN number_format TEXT NOT NULL DEFAULT '1.234,56' CHECK (number_format IN ('1.234,56', '1,234.56'));
ALTER TABLE accounts ADD COLUMN currency TEXT NOT NULL DEFAULT 'BRL' CHECK (currency IN ('BRL', 'USD', 'EUR'));

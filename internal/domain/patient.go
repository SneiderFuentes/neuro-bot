package domain

import "time"

type Patient struct {
	ID             string
	DocumentType   string
	DocumentNumber string
	FirstName      string
	SecondName     string
	FirstSurname   string
	SecondSurname  string
	FullName       string
	BirthDate      time.Time
	Gender         string // F/M
	Phone          string
	Email          string
	Address        string
	CityCode       string
	Zone           string // U/R
	EntityCode     string
	AffiliationType string
	UserType       string
	Occupation     string
	Level          string
	MaritalStatus  string
	BirthPlace     string
	EducationLevel string
	CountryCode    string
}

type CreatePatientInput struct {
	DocumentType    string
	DocumentNumber  string
	FirstName       string
	SecondName      string
	FirstSurname    string
	SecondSurname   string
	BirthDate       time.Time
	Gender          string
	Phone           string
	Email           string
	Address         string
	CityCode        string
	Zone            string
	EntityCode      string
	AffiliationType string
	UserType        string
	Occupation      string
	Level           string
	MaritalStatus   string
	BirthPlace      string
	EducationLevel  string
	CountryCode     string
}
